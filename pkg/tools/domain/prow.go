package domain

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/openshift-eng/openshift-ci-mcp/pkg/client"
	"github.com/openshift-eng/openshift-ci-mcp/pkg/tools"
)

func runParams(decks string) []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithString("run_url", mcp.Description("Prow URL of the run: the Details link of a PR check, a Spyglass or gcsweb URL. Preferred over job_name/build_id.")),
		mcp.WithString("job_name", mcp.Description("Job name, with build_id, when run_url is not given")),
		mcp.WithString("build_id", mcp.Description("Build ID of the run, with job_name")),
		mcp.WithString("pr", mcp.Description("PR number, for presubmit runs given by job_name (derived for rehearse-<pr>-* jobs)")),
		mcp.WithString("org_repo", mcp.Description("Repository of the PR as org_repo or org/repo, for presubmit runs given by job_name (default openshift_release for rehearse-* jobs)")),
		mcp.WithString("deck", mcp.Description("Deck of the run when run_url is not given: "+decks)),
	}
}

func RegisterProwTools(s *server.MCPServer, prow client.Prow, cache *client.ResponseCache) {
	decks := deckChoices(prow.Decks())
	s.AddTool(
		mcp.NewTool("get_prow_job_history",
			mcp.WithDescription("Use to list all runs of a Prow job, including pending and aborted runs, presubmits and /pj-rehearse runs that Sippy does not track"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
			mcp.WithString("job_name", mcp.Required(), mcp.Description("Exact Prow job name")),
			mcp.WithString("deck", mcp.Description("Deck the job reports to: "+decks)),
			mcp.WithString("job_type", mcp.Description("presubmit, postsubmit or periodic. Default: presubmit for pull-ci-* and rehearse-* jobs, otherwise periodic")),
			mcp.WithString("pr", mcp.Description("Only runs for this PR number (presubmits)")),
			mcp.WithNumber("limit", mcp.Description("Max runs to return (default 20)"), mcp.DefaultNumber(20)),
			mcp.WithString("start_build_id", mcp.Description("Start from this build ID and go back in time; pass next_start_build_id from a previous call to page")),
		),
		GetProwJobHistoryHandler(prow, cache),
	)

	s.AddTool(
		mcp.NewTool("get_prow_job_run",
			append([]mcp.ToolOption{
				mcp.WithDescription("Use to get one Prow job run's result, timing, refs and the pass/fail of each step, with failure excerpts and the test directories that hold step logs"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(true),
			}, runParams(decks)...)...,
		),
		GetProwJobRunHandler(prow, cache),
	)

	s.AddTool(
		mcp.NewTool("get_prow_artifacts",
			append([]mcp.ToolOption{
				mcp.WithDescription("Use to list the files and directories of a Prow job run, such as artifacts/<test>/<step>/"),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(true),
				mcp.WithString("path", mcp.Description("Directory relative to the run root (default: artifacts/)")),
			}, runParams(decks)...)...,
		),
		GetProwArtifactsHandler(prow),
	)

	s.AddTool(
		mcp.NewTool("get_prow_artifact_content",
			append([]mcp.ToolOption{
				mcp.WithDescription("Use to read a Prow job run's build log, a step's build log, or any text artifact, with tail, head or grep to keep the output small. Defaults to the last 200 lines of the run's build-log.txt."),
				mcp.WithReadOnlyHintAnnotation(true),
				mcp.WithDestructiveHintAnnotation(false),
				mcp.WithIdempotentHintAnnotation(true),
				mcp.WithOpenWorldHintAnnotation(true),
				mcp.WithString("path", mcp.Description("File relative to the run root, e.g. artifacts/junit_operator.xml")),
				mcp.WithString("step", mcp.Description("Step name: reads artifacts/<test>/<step>/build-log.txt (instead of path)")),
				mcp.WithString("test", mcp.Description("Test directory of the step (default: the run's only test directory)")),
				mcp.WithString("grep", mcp.Description("Regular expression; return only matching lines with context (overrides head/tail)")),
				mcp.WithNumber("context_lines", mcp.Description("Lines of context around grep matches (default 3)")),
				mcp.WithNumber("max_matches", mcp.Description("Max grep matches (default 50)")),
				mcp.WithNumber("head_lines", mcp.Description("Return the first N lines instead of the tail")),
				mcp.WithNumber("tail_lines", mcp.Description("Return the last N lines (default 200)")),
				mcp.WithNumber("max_bytes", mcp.Description("Max bytes of text returned (default 65536, max 1048576)")),
			}, runParams(decks)...)...,
		),
		GetProwArtifactContentHandler(prow),
	)
}

type prowRun struct {
	BuildID         string  `json:"build_id"`
	Started         string  `json:"started"`
	DurationSeconds float64 `json:"duration_seconds"`
	Result          string  `json:"result"`
	PR              int     `json:"pr,omitempty"`
	PRSHA           string  `json:"pr_sha,omitempty"`
	BaseRef         string  `json:"base_ref,omitempty"`
	BaseSHA         string  `json:"base_sha,omitempty"`
	URL             string  `json:"url"`
}

type prowJobHistory struct {
	Job              string    `json:"job"`
	Deck             string    `json:"deck"`
	Runs             []prowRun `json:"runs"`
	NextStartBuildID string    `json:"next_start_build_id,omitempty"`
	PagesScanned     int       `json:"pages_scanned"`
	HistoryURL       string    `json:"history_url"`
}

// maxHistoryPages bounds how many job-history pages (20 runs each) one
// call reads while filling the limit or filtering by PR.
const maxHistoryPages = 10

func GetProwJobHistoryHandler(prow client.Prow, cache *client.ResponseCache) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		job, err := req.RequireString("job_name")
		if err != nil || strings.TrimSpace(job) == "" {
			return tools.InvalidParam("job_name", "required")
		}
		deck, err := client.DeckByName(prow.Decks(), req.GetString("deck", "public"))
		if err != nil {
			return tools.InvalidParam("deck", err.Error())
		}
		dir, err := historyDir(job, req.GetString("job_type", ""))
		if err != nil {
			return tools.InvalidParam("job_type", err.Error())
		}
		limit := req.GetInt("limit", 20)
		if limit < 1 {
			limit = 1
		}
		var prFilter int
		if pr := req.GetString("pr", ""); pr != "" {
			if prFilter, err = strconv.Atoi(pr); err != nil {
				return tools.InvalidParam("pr", "must be a number")
			}
		}

		result := prowJobHistory{
			Job:        job,
			Deck:       deck.Name,
			Runs:       []prowRun{},
			HistoryURL: fmt.Sprintf("%s/job-history/gs/%s/%s/%s", deck.DeckURL, deck.Bucket(), dir, url.PathEscape(job)),
		}
		start := req.GetString("start_build_id", "")
		for {
			page, err := jobHistoryPage(ctx, prow, cache, deck, dir, job, start)
			if err != nil {
				return prowToolError(err)
			}
			result.PagesScanned++
			for _, b := range page.Builds {
				if len(result.Runs) == limit {
					result.NextStartBuildID = b.ID
					break
				}
				if run := toProwRun(deck, b); prFilter == 0 || run.PR == prFilter {
					result.Runs = append(result.Runs, run)
				}
			}
			if result.NextStartBuildID != "" || page.OlderBuildID == "" {
				break
			}
			start = page.OlderBuildID
			if len(result.Runs) == limit || result.PagesScanned == maxHistoryPages {
				result.NextStartBuildID = start
				break
			}
		}
		return jsonResult(result)
	}
}

func historyDir(job, jobType string) (string, error) {
	switch jobType {
	case "presubmit":
		return "pr-logs/directory", nil
	case "postsubmit", "periodic":
		return "logs", nil
	case "":
		if strings.HasPrefix(job, "pull-ci-") || strings.HasPrefix(job, "rehearse-") {
			return "pr-logs/directory", nil
		}
		return "logs", nil
	}
	return "", fmt.Errorf("must be presubmit, postsubmit or periodic")
}

func jobHistoryPage(ctx context.Context, prow client.Prow, cache *client.ResponseCache, deck client.Deck, dir, job, before string) (*client.JobHistoryPage, error) {
	if before == "" {
		// The newest page changes as runs start and finish; callers poll it.
		cache = nil
	}
	key := fmt.Sprintf("prow_job_history:%s:%s:%s:%s", deck.Name, dir, job, before)
	data, err := cache.GetOrFetch(key, func() ([]byte, error) {
		page, err := prow.JobHistory(ctx, deck, dir, job, before)
		if err != nil {
			return nil, err
		}
		return json.Marshal(page)
	})
	if err != nil {
		return nil, err
	}
	var page client.JobHistoryPage
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

func toProwRun(deck client.Deck, b client.JobHistoryBuild) prowRun {
	run := prowRun{
		BuildID:         b.ID,
		Result:          b.Result,
		DurationSeconds: b.Duration.Seconds(),
		URL:             deck.DeckURL + b.SpyglassLink,
	}
	if !b.Started.IsZero() {
		run.Started = b.Started.UTC().Format(time.RFC3339)
	}
	if b.Refs != nil {
		run.BaseRef = b.Refs.BaseRef
		run.BaseSHA = b.Refs.BaseSHA
		if len(b.Refs.Pulls) > 0 {
			run.PR = b.Refs.Pulls[0].Number
			run.PRSHA = b.Refs.Pulls[0].SHA
		}
	}
	return run
}

// resolveRun reads the run identification parameters shared by the run tools.
func resolveRun(req mcp.CallToolRequest, prow client.Prow) (client.RunRef, *mcp.CallToolResult) {
	if runURL := req.GetString("run_url", ""); runURL != "" {
		ref, err := client.ParseRunURL(prow.Decks(), runURL)
		if err != nil {
			res, _ := tools.InvalidParam("run_url", err.Error())
			return client.RunRef{}, res
		}
		return ref, nil
	}
	job, buildID := req.GetString("job_name", ""), req.GetString("build_id", "")
	if job == "" || buildID == "" {
		res, _ := tools.InvalidParam("run_url", "give run_url, or job_name and build_id")
		return client.RunRef{}, res
	}
	deck, err := client.DeckByName(prow.Decks(), req.GetString("deck", "public"))
	if err != nil {
		res, _ := tools.InvalidParam("deck", err.Error())
		return client.RunRef{}, res
	}
	ref, err := client.NewRunRef(deck, job, buildID, req.GetString("pr", ""), req.GetString("org_repo", ""))
	if err != nil {
		res, _ := tools.InvalidParam("job_name", err.Error())
		return client.RunRef{}, res
	}
	return ref, nil
}

type prowStep struct {
	Name            string  `json:"name"`
	Passed          bool    `json:"passed"`
	DurationSeconds float64 `json:"duration_seconds"`
	Failure         string  `json:"failure,omitempty"`
}

type prowJobRun struct {
	Job             string            `json:"job"`
	BuildID         string            `json:"build_id"`
	Deck            string            `json:"deck"`
	State           string            `json:"state"` // finished or running
	Result          string            `json:"result,omitempty"`
	Started         string            `json:"started,omitempty"`
	Finished        string            `json:"finished,omitempty"`
	DurationSeconds float64           `json:"duration_seconds,omitempty"`
	JobType         string            `json:"job_type,omitempty"`
	Refs            *prowRefs         `json:"refs,omitempty"`
	Steps           []prowStep        `json:"steps,omitempty"`
	FailedSteps     []string          `json:"failed_steps,omitempty"`
	TestDirs        []string          `json:"test_dirs,omitempty"`
	URL             string            `json:"url"`
	ArtifactsURL    string            `json:"artifacts_url"`
	Notes           []string          `json:"notes,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type prowRefs struct {
	Org     string `json:"org,omitempty"`
	Repo    string `json:"repo,omitempty"`
	BaseRef string `json:"base_ref,omitempty"`
	BaseSHA string `json:"base_sha,omitempty"`
	PR      int    `json:"pr,omitempty"`
	PRSHA   string `json:"pr_sha,omitempty"`
	PRTitle string `json:"pr_title,omitempty"`
}

type prowStarted struct {
	Timestamp int64 `json:"timestamp"`
}

type prowFinished struct {
	Timestamp int64  `json:"timestamp"`
	Passed    *bool  `json:"passed"`
	Result    string `json:"result"`
	Revision  string `json:"revision"`
}

type prowJobSpec struct {
	Spec struct {
		Type string `json:"type"`
		Job  string `json:"job"`
		Refs *struct {
			Org     string `json:"org"`
			Repo    string `json:"repo"`
			BaseRef string `json:"base_ref"`
			BaseSHA string `json:"base_sha"`
			Pulls   []struct {
				Number int    `json:"number"`
				SHA    string `json:"sha"`
				Title  string `json:"title"`
			} `json:"pulls"`
		} `json:"refs"`
	} `json:"spec"`
	Status struct {
		State          string    `json:"state"`
		CompletionTime time.Time `json:"completionTime"`
	} `json:"status"`
}

type junitSuites struct {
	Suites []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Cases []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name    string  `xml:"name,attr"`
	Time    float64 `xml:"time,attr"`
	Failure *struct {
		Message string `xml:"message,attr"`
		Text    string `xml:",chardata"`
	} `xml:"failure"`
	Skipped *struct{} `xml:"skipped"`
}

// terminalStates are the prowjob states of a run that has ended.
var terminalStates = map[string]bool{"success": true, "failure": true, "aborted": true, "error": true}

// maxFailureExcerpt bounds the failure text kept per step.
const maxFailureExcerpt = 600

// ci-operator directories under artifacts/ that are not tests.
var nonTestDirs = map[string]bool{"build-logs": true, "build-resources": true, "release": true, "junit": true}

func GetProwJobRunHandler(prow client.Prow, cache *client.ResponseCache) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ref, errRes := resolveRun(req, prow)
		if errRes != nil {
			return errRes, nil
		}
		key := "prow_job_run:" + ref.Deck.Name + ":" + ref.Path
		data, err := cache.GetOrFetch(key, func() ([]byte, error) {
			run, err := fetchJobRun(ctx, prow, ref)
			if err != nil {
				return nil, err
			}
			if run.State != "finished" {
				// Don't cache a running job; return it through the error path.
				return nil, &runningJob{run: run}
			}
			return json.Marshal(run)
		})
		var running *runningJob
		if errors.As(err, &running) {
			return jsonResult(running.run)
		}
		if err != nil {
			return prowToolError(err)
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

type runningJob struct{ run prowJobRun }

func (r *runningJob) Error() string { return "job is still running" }

func fetchJobRun(ctx context.Context, prow client.Prow, ref client.RunRef) (prowJobRun, error) {
	run := prowJobRun{
		Job:          ref.Job,
		BuildID:      ref.BuildID,
		Deck:         ref.Deck.Name,
		URL:          ref.SpyglassURL(),
		ArtifactsURL: ref.ObjectURL("artifacts/"),
	}

	var (
		wg                         sync.WaitGroup
		started, finished, prowjob []byte
		junit                      []byte
		listing                    *client.Listing
		startedErr, finishedErr    error
		prowjobErr, junitErr       error
		listErr                    error
	)
	read := func(rel string, dst *[]byte, dstErr *error) {
		defer wg.Done()
		*dst, *dstErr = readSmall(ctx, prow, ref, rel)
	}
	wg.Add(5)
	go read("started.json", &started, &startedErr)
	go read("finished.json", &finished, &finishedErr)
	go read("prowjob.json", &prowjob, &prowjobErr)
	go read("artifacts/junit_operator.xml", &junit, &junitErr)
	go func() {
		defer wg.Done()
		listing, listErr = prow.List(ctx, ref.Deck, ref.Path+"/artifacts/")
	}()
	wg.Wait()

	if startedErr != nil && finishedErr != nil && prowjobErr != nil {
		return run, startedErr
	}
	// A failed read other than "not found" must not turn into a cached
	// result that lacks data.
	for _, err := range []error{startedErr, finishedErr, prowjobErr} {
		if err != nil && !isNotFound(err) {
			return run, err
		}
	}

	var st prowStarted
	if startedErr == nil && json.Unmarshal(started, &st) == nil && st.Timestamp > 0 {
		run.Started = time.Unix(st.Timestamp, 0).UTC().Format(time.RFC3339)
	}
	run.State = "running"
	var fin prowFinished
	if finishedErr == nil && json.Unmarshal(finished, &fin) == nil && fin.Timestamp > 0 {
		run.State = "finished"
		run.Result = fin.Result
		if run.Result == "" && fin.Passed != nil {
			run.Result = map[bool]string{true: "SUCCESS", false: "FAILURE"}[*fin.Passed]
		}
		run.Finished = time.Unix(fin.Timestamp, 0).UTC().Format(time.RFC3339)
		if st.Timestamp > 0 {
			run.DurationSeconds = float64(fin.Timestamp - st.Timestamp)
		}
	}

	var pj prowJobSpec
	if prowjobErr == nil && json.Unmarshal(prowjob, &pj) == nil {
		run.JobType = pj.Spec.Type
		if pj.Spec.Refs != nil {
			r := pj.Spec.Refs
			run.Refs = &prowRefs{Org: r.Org, Repo: r.Repo, BaseRef: r.BaseRef, BaseSHA: r.BaseSHA}
			if len(r.Pulls) > 0 {
				run.Refs.PR, run.Refs.PRSHA, run.Refs.PRTitle = r.Pulls[0].Number, r.Pulls[0].SHA, r.Pulls[0].Title
			}
		}
		if run.State == "running" && terminalStates[pj.Status.State] {
			// The pod ended without uploading finished.json, e.g. when aborted.
			run.State = "finished"
			run.Result = strings.ToUpper(pj.Status.State)
			if !pj.Status.CompletionTime.IsZero() {
				run.Finished = pj.Status.CompletionTime.UTC().Format(time.RFC3339)
				if st.Timestamp > 0 {
					run.DurationSeconds = float64(pj.Status.CompletionTime.Unix() - st.Timestamp)
				}
			}
			run.Notes = append(run.Notes, "finished.json is missing; result taken from prowjob.json")
		}
	}

	if junitErr == nil {
		steps, err := parseJunitSteps(junit)
		if err != nil {
			run.Notes = append(run.Notes, "could not parse junit_operator.xml: "+err.Error())
		}
		run.Steps = steps
		for _, s := range steps {
			if !s.Passed {
				run.FailedSteps = append(run.FailedSteps, s.Name)
			}
		}
	} else if !isNotFound(junitErr) {
		return run, fmt.Errorf("reading artifacts/junit_operator.xml: %w", junitErr)
	} else if run.State == "finished" {
		run.Notes = append(run.Notes, "no artifacts/junit_operator.xml: step results unavailable")
	}

	if listErr != nil && !isNotFound(listErr) {
		return run, fmt.Errorf("listing artifacts/: %w", listErr)
	}
	if listErr == nil {
		for _, e := range listing.Entries {
			if e.Type == "dir" && e.Name != ".." && !nonTestDirs[e.Name] {
				run.TestDirs = append(run.TestDirs, e.Name)
			}
		}
	}
	return run, nil
}

func isNotFound(err error) bool {
	var httpErr *client.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound
}

// readSmall reads a small metadata object of a run (JSON or junit XML).
func readSmall(ctx context.Context, prow client.Prow, ref client.RunRef, rel string) ([]byte, error) {
	obj, err := prow.Open(ctx, ref.Deck, ref.Path+"/"+rel)
	if err != nil {
		return nil, err
	}
	defer obj.Body.Close()
	return io.ReadAll(io.LimitReader(obj.Body, 4<<20))
}

func parseJunitSteps(data []byte) ([]prowStep, error) {
	var suites junitSuites
	if err := xml.Unmarshal(data, &suites); err != nil {
		return nil, err
	}
	if len(suites.Suites) == 0 {
		// A file with a single <testsuite> root decodes into junitSuites
		// without error but with no suites.
		var suite junitSuite
		if err := xml.Unmarshal(data, &suite); err != nil {
			return nil, err
		}
		suites.Suites = []junitSuite{suite}
	}
	var steps []prowStep
	for _, s := range suites.Suites {
		for _, c := range s.Cases {
			if strings.TrimSpace(c.Name) == "" || c.Skipped != nil {
				continue
			}
			step := prowStep{Name: c.Name, Passed: c.Failure == nil, DurationSeconds: c.Time}
			if c.Failure != nil {
				msg := strings.TrimSpace(c.Failure.Text)
				if msg == "" {
					msg = strings.TrimSpace(c.Failure.Message)
				}
				step.Failure = excerpt(msg, maxFailureExcerpt)
			}
			steps = append(steps, step)
		}
	}
	// Failed steps first, keeping junit order otherwise.
	sort.SliceStable(steps, func(i, j int) bool { return !steps[i].Passed && steps[j].Passed })
	return steps, nil
}

// excerpt keeps the start and the end of a long failure text: ci-operator
// puts the reason first and the step's log tail after it.
func excerpt(s string, n int) string {
	s = ansiEscape.ReplaceAllString(s, "")
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n/2], "") + "\n…\n" + strings.ToValidUTF8(s[len(s)-n/2:], "")
}

type prowArtifacts struct {
	Path      string                 `json:"path"`
	Entries   []client.ArtifactEntry `json:"entries"`
	Truncated bool                   `json:"truncated,omitempty"`
	URL       string                 `json:"url"`
}

func GetProwArtifactsHandler(prow client.Prow) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ref, errRes := resolveRun(req, prow)
		if errRes != nil {
			return errRes, nil
		}
		rel, err := cleanRelPath(req.GetString("path", "artifacts/"))
		if err != nil {
			return tools.InvalidParam("path", err.Error())
		}
		listing, err := prow.List(ctx, ref.Deck, ref.Path+"/"+rel)
		if err != nil {
			return prowToolError(err)
		}
		out := prowArtifacts{Path: rel, Entries: []client.ArtifactEntry{}, Truncated: listing.Truncated, URL: ref.ObjectURL(rel)}
		prefix := ref.Path + "/"
		for _, e := range listing.Entries {
			if e.Name == ".." || !strings.HasPrefix(e.Path, prefix) || len(e.Path) <= len(prefix+rel) {
				continue // parent directory link
			}
			e.Path = strings.TrimPrefix(e.Path, prefix)
			out.Entries = append(out.Entries, e)
		}
		return jsonResult(out)
	}
}

func cleanRelPath(p string) (string, error) {
	p = strings.TrimPrefix(strings.TrimSpace(p), "/")
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || seg == "." {
			return "", fmt.Errorf("must stay inside the run (no . or .. segments)")
		}
	}
	if p != "" && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p, nil
}

type prowArtifactContent struct {
	Path string `json:"path"`
	URL  string `json:"url"`
	textResult
}

var stepName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// plainName accepts one path segment that is not . or ..
func plainName(s string) bool {
	return stepName.MatchString(s) && s != "." && s != ".."
}

func GetProwArtifactContentHandler(prow client.Prow) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ref, errRes := resolveRun(req, prow)
		if errRes != nil {
			return errRes, nil
		}

		rel := strings.TrimPrefix(strings.TrimSpace(req.GetString("path", "")), "/")
		step := req.GetString("step", "")
		switch {
		case rel != "" && step != "":
			return tools.InvalidParam("step", "give path or step, not both")
		case step != "":
			test := req.GetString("test", "")
			if !plainName(step) || (test != "" && !plainName(test)) {
				return tools.InvalidParam("step", "step and test must be plain names")
			}
			if test == "" {
				var err error
				if test, err = onlyTestDir(ctx, prow, ref); err != nil {
					var authErr *client.AuthError
					if errors.As(err, &authErr) {
						return prowToolError(err)
					}
					return tools.InvalidParam("test", err.Error())
				}
			}
			rel = fmt.Sprintf("artifacts/%s/%s/build-log.txt", test, step)
		case rel == "":
			rel = "build-log.txt"
		default:
			if strings.HasSuffix(rel, "/") {
				return tools.InvalidParam("path", "is a directory; use get_prow_artifacts")
			}
			if _, err := cleanRelPath(rel); err != nil {
				return tools.InvalidParam("path", err.Error())
			}
		}

		sel := textSelection{
			// The output holds at most maxMaxBytes lines, so larger counts
			// would only make the line buffers grow.
			HeadLines:    min(req.GetInt("head_lines", 0), maxMaxBytes),
			TailLines:    min(req.GetInt("tail_lines", defaultTailLines), maxMaxBytes),
			ContextLines: min(req.GetInt("context_lines", defaultContextLines), maxMaxBytes),
			MaxMatches:   min(req.GetInt("max_matches", defaultMaxMatches), maxMaxBytes),
			MaxBytes:     req.GetInt("max_bytes", defaultMaxBytes),
		}
		if sel.MaxBytes <= 0 || sel.MaxBytes > maxMaxBytes {
			sel.MaxBytes = min(max(sel.MaxBytes, defaultMaxBytes), maxMaxBytes)
		}
		if sel.ContextLines < 0 {
			sel.ContextLines = 0
		}
		if sel.MaxMatches <= 0 {
			sel.MaxMatches = defaultMaxMatches
		}
		if pattern := req.GetString("grep", ""); pattern != "" {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return tools.InvalidParam("grep", err.Error())
			}
			sel.Grep = re
		}

		obj, err := prow.Open(ctx, ref.Deck, ref.Path+"/"+rel)
		if err != nil {
			var httpErr *client.HTTPError
			if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
				return tools.ToolError(fmt.Errorf("%s not found in run %s/%s (it may not exist, or the step has not finished yet)", rel, ref.Job, ref.BuildID))
			}
			return prowToolError(err)
		}
		defer obj.Body.Close()

		br := bufio.NewReader(obj.Body)
		head, _ := br.Peek(512)
		out := prowArtifactContent{Path: rel, URL: ref.ObjectURL(rel)}
		if looksBinary(obj.ContentType, head) {
			out.Mode = "binary"
			out.Text = "binary content (" + obj.ContentType + "); open the URL instead"
			return jsonResult(out)
		}
		res, err := selectText(br, sel)
		if err != nil {
			return tools.ToolError(fmt.Errorf("reading %s: %w", rel, err))
		}
		out.textResult = res
		return jsonResult(out)
	}
}

// onlyTestDir returns the run's test directory under artifacts/ when there
// is exactly one.
func onlyTestDir(ctx context.Context, prow client.Prow, ref client.RunRef) (string, error) {
	listing, err := prow.List(ctx, ref.Deck, ref.Path+"/artifacts/")
	if err != nil {
		return "", fmt.Errorf("listing artifacts/ to find the test: %w", err)
	}
	var dirs []string
	for _, e := range listing.Entries {
		if e.Type == "dir" && e.Name != ".." && !nonTestDirs[e.Name] {
			dirs = append(dirs, e.Name)
		}
	}
	if len(dirs) != 1 {
		return "", fmt.Errorf("set test to one of: %s", strings.Join(dirs, ", "))
	}
	return dirs[0], nil
}

// deckChoices describes the deck names for the deck parameter.
func deckChoices(decks []client.Deck) string {
	names := []string{"public (default)"}
	for _, d := range decks {
		if d.Name != "public" {
			names = append(names, d.Name)
		}
	}
	return strings.Join(names, ", ")
}

// prowToolError reports a missing or rejected deck token as HTTP 401.
func prowToolError(err error) (*mcp.CallToolResult, error) {
	var authErr *client.AuthError
	if errors.As(err, &authErr) {
		return tools.ToolError(&client.HTTPError{StatusCode: http.StatusUnauthorized, Body: []byte(authErr.Error())})
	}
	return tools.ToolError(err)
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return tools.ToolError(err)
	}
	return mcp.NewToolResultText(string(data)), nil
}
