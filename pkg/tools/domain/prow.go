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

func runParams() []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithString("run_url", mcp.Description("Prow URL of the run: the Details link of a PR check, a Spyglass or gcsweb URL. Preferred over job_name/build_id.")),
		mcp.WithString("job_name", mcp.Description("Job name, with build_id, when run_url is not given")),
		mcp.WithString("build_id", mcp.Description("Build ID of the run, with job_name")),
		mcp.WithString("pr", mcp.Description("PR number, for presubmit runs given by job_name (derived for rehearse-<pr>-* jobs)")),
		mcp.WithString("org_repo", mcp.Description("Repository of the PR as org_repo or org/repo, for presubmit runs given by job_name (default openshift_release for rehearse-* jobs)")),
		mcp.WithString("deck", mcp.Description("Deck of the run when run_url is not given (default: public)")),
	}
}

func RegisterProwTools(s *server.MCPServer, prow client.Prow, cache *client.ResponseCache) {
	s.AddTool(
		mcp.NewTool("get_prow_job_history",
			mcp.WithDescription("Use to list all runs of a Prow job, including pending and aborted runs, presubmits and /pj-rehearse runs that Sippy does not track"),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(true),
			mcp.WithString("job_name", mcp.Required(), mcp.Description("Exact Prow job name")),
			mcp.WithString("deck", mcp.Description("Deck the job reports to (default: public)")),
			mcp.WithString("job_type", mcp.Description("presubmit, postsubmit or periodic. Default: presubmit for pull-ci-* and rehearse-* jobs, otherwise periodic")),
			mcp.WithString("pr", mcp.Description("Only runs for this PR number (presubmits); needs org_repo unless the job is rehearse-*. Batch runs that tested the PR with others are not included")),
			mcp.WithString("org_repo", mcp.Description("Repository of the PR as org_repo or org/repo, with pr (default openshift_release for rehearse-* jobs)")),
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
			}, runParams()...)...,
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
			}, runParams()...)...,
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
				mcp.WithString("step", mcp.Description("Step name, as in the step field of get_prow_job_run: reads artifacts/<test>/<step>/build-log.txt (instead of path)")),
				mcp.WithString("test", mcp.Description("Test directory of the step (default: the run's only test directory)")),
				mcp.WithString("grep", mcp.Description("Regular expression; return only matching lines with context (overrides head/tail)")),
				mcp.WithNumber("context_lines", mcp.Description("Lines of context around grep matches (default 3)")),
				mcp.WithNumber("max_matches", mcp.Description("Max grep matches (default 50)")),
				mcp.WithNumber("head_lines", mcp.Description("Return the first N lines instead of the tail")),
				mcp.WithNumber("tail_lines", mcp.Description("Return the last N lines (default 200)")),
				mcp.WithNumber("max_bytes", mcp.Description("Max bytes of text returned (default 65536, max 1048576)")),
			}, runParams()...)...,
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
	PagesScanned     int       `json:"pages_scanned,omitempty"`
	// Truncated is set when the page cap stopped the scan before limit
	// runs were found, so older runs may exist.
	Truncated  bool   `json:"truncated,omitempty"`
	Note       string `json:"note,omitempty"`
	HistoryURL string `json:"history_url"`
}

// maxHistoryPages bounds how many job-history pages (20 runs each) one
// call reads while filling the limit.
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
		start := req.GetString("start_build_id", "")
		if start != "" && !buildID.MatchString(start) {
			return tools.InvalidParam("start_build_id", "must be a numeric build ID")
		}
		pr, orgRepo := strings.TrimSpace(req.GetString("pr", "")), strings.TrimSpace(req.GetString("org_repo", ""))
		if pr != "" {
			if !buildID.MatchString(pr) {
				return tools.InvalidParam("pr", "must be a number")
			}
			if dir != "pr-logs/directory" {
				return tools.InvalidParam("pr", "only presubmit jobs run for a PR")
			}
			return prJobHistory(ctx, prow, deck, job, pr, orgRepo, start, limit)
		}
		if orgRepo != "" {
			return tools.InvalidParam("org_repo", "is used only with pr")
		}

		result := prowJobHistory{
			Job:        job,
			Deck:       deck.Name,
			Runs:       []prowRun{},
			HistoryURL: fmt.Sprintf("%s/job-history/gs/%s/%s/%s", deck.DeckURL, deck.Bucket(), dir, url.PathEscape(job)),
		}
		var oldest string
		for {
			page, err := jobHistoryPage(ctx, prow, cache, deck, dir, job, start)
			if err != nil {
				return tools.ToolError(err)
			}
			result.PagesScanned++
			for _, b := range page.Builds {
				if len(result.Runs) == limit {
					result.NextStartBuildID = b.ID
					break
				}
				if !b.Started.IsZero() {
					oldest = b.Started.UTC().Format(time.RFC3339)
				}
				result.Runs = append(result.Runs, toProwRun(deck, b))
			}
			if result.NextStartBuildID != "" || page.OlderBuildID == "" {
				break
			}
			start = page.OlderBuildID
			if len(result.Runs) == limit {
				result.NextStartBuildID = start
				break
			}
			if result.PagesScanned == maxHistoryPages {
				result.NextStartBuildID = start
				result.Truncated = true
				back := ""
				if oldest != "" {
					back = " (runs back to " + oldest + ")"
				}
				result.Note = fmt.Sprintf("stopped after %d pages of history%s with %d of %d runs found; older runs may exist, call again with start_build_id=%s",
					result.PagesScanned, back, len(result.Runs), limit, start)
				break
			}
		}
		return jsonResult(result)
	}
}

// maxParallelRuns bounds how many runs prJobHistory reads at once.
const maxParallelRuns = 8

// prJobHistory lists the runs of a presubmit job for one PR from the PR's
// own directory, pr-logs/pull/<org_repo>/<pr>/<job>/, which holds only that
// PR's runs: scanning the job's history for them can take minutes on busy
// jobs and still miss older runs.
func prJobHistory(ctx context.Context, prow client.Prow, deck client.Deck, job, pr, orgRepo, start string, limit int) (*mcp.CallToolResult, error) {
	jobDir, err := client.PRJobPath(job, pr, orgRepo)
	if err != nil {
		return tools.InvalidParam("org_repo", err.Error())
	}
	var startID uint64
	if start != "" {
		if startID, err = strconv.ParseUint(start, 10, 64); err != nil {
			return tools.InvalidParam("start_build_id", "must be a numeric build ID")
		}
	}
	result := prowJobHistory{
		Job:        job,
		Deck:       deck.Name,
		Runs:       []prowRun{},
		HistoryURL: fmt.Sprintf("%s/gcs/%s/%s/", deck.GCSWebURL, deck.Bucket(), jobDir),
	}
	listing, err := prow.List(ctx, deck, jobDir+"/")
	if isNotFound(err) {
		result.Note = fmt.Sprintf("no runs of this job for PR %s: %s/ does not exist (check pr and org_repo)", pr, jobDir)
		return jsonResult(result)
	}
	if err != nil {
		return tools.ToolError(err)
	}
	var ids []uint64
	for _, e := range listing.Entries {
		id, err := strconv.ParseUint(e.Name, 10, 64)
		if e.Type != "dir" || err != nil || (startID != 0 && id > startID) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] })
	if len(ids) > limit {
		result.NextStartBuildID = strconv.FormatUint(ids[limit], 10)
		ids = ids[:limit]
	}
	if listing.Truncated {
		// gcsweb lists in ascending order, so the pages not read hold the
		// newest runs.
		result.Truncated = true
		result.Note = "the PR directory is too large to list in full; the newest runs may be missing"
	}

	refs := make([]client.RunRef, len(ids))
	for i, id := range ids {
		if refs[i], err = client.NewRunRef(deck, job, strconv.FormatUint(id, 10), pr, orgRepo); err != nil {
			return tools.ToolError(err)
		}
	}
	// The first failed read cancels the others.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	runs := make([]prowRun, len(refs))
	errs := make([]error, len(refs))
	sem := make(chan struct{}, maxParallelRuns)
	var wg sync.WaitGroup
	for i, ref := range refs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			if runs[i], errs[i] = prRunSummary(ctx, prow, ref); errs[i] != nil {
				cancel()
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		// Reads stopped by the cancel report context.Canceled; return the
		// failure that caused it.
		if err != nil && !errors.Is(err, context.Canceled) {
			return tools.ToolError(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return tools.ToolError(err)
	}
	result.Runs = runs
	return jsonResult(result)
}

// prRunSummary reads a history row for one run from its started.json and
// finished.json, and from prowjob.json when finished.json is missing. A run
// that has not ended is PENDING, as on deck.
func prRunSummary(ctx context.Context, prow client.Prow, ref client.RunRef) (prowRun, error) {
	var (
		wg                      sync.WaitGroup
		started, finished       []byte
		startedErr, finishedErr error
	)
	wg.Add(2)
	go func() { defer wg.Done(); started, startedErr = readSmall(ctx, prow, ref, "started.json") }()
	go func() { defer wg.Done(); finished, finishedErr = readSmall(ctx, prow, ref, "finished.json") }()
	wg.Wait()
	for _, err := range []error{startedErr, finishedErr} {
		if err != nil && !isNotFound(err) {
			return prowRun{}, fmt.Errorf("reading run %s: %w", ref.BuildID, err)
		}
	}

	run := prowRun{BuildID: ref.BuildID, Result: "PENDING", URL: ref.SpyglassURL()}
	run.PR, _ = strconv.Atoi(ref.PR)
	var st prowStarted
	if startedErr == nil && json.Unmarshal(started, &st) == nil {
		if st.Timestamp > 0 {
			run.Started = time.Unix(st.Timestamp, 0).UTC().Format(time.RFC3339)
		}
		run.BaseRef, run.BaseSHA, run.PRSHA = parseStartedRepos(st.Repos, ref.PR)
	}
	end, ok := finishedEnd(finished, finishedErr)
	if !ok {
		prowjob, err := readSmall(ctx, prow, ref, "prowjob.json")
		if err != nil && !isNotFound(err) {
			return prowRun{}, fmt.Errorf("reading run %s: %w", ref.BuildID, err)
		}
		var pj prowJobSpec
		if err == nil && json.Unmarshal(prowjob, &pj) == nil {
			end, ok = prowJobEnd(pj)
		}
	}
	if ok {
		run.Result = end.Result
		run.DurationSeconds = end.duration(st)
	}
	return run, nil
}

// runEnd is how and when a run ended.
type runEnd struct {
	Result   string
	Finished time.Time // zero when unknown
}

func (e runEnd) duration(st prowStarted) float64 {
	if st.Timestamp <= 0 || e.Finished.IsZero() {
		return 0
	}
	return float64(e.Finished.Unix() - st.Timestamp)
}

// finishedEnd reads the end of a run from finished.json.
func finishedEnd(finished []byte, err error) (runEnd, bool) {
	var fin prowFinished
	if err != nil || json.Unmarshal(finished, &fin) != nil || fin.Timestamp <= 0 {
		return runEnd{}, false
	}
	end := runEnd{Result: fin.Result, Finished: time.Unix(fin.Timestamp, 0)}
	if end.Result == "" && fin.Passed != nil {
		end.Result = map[bool]string{true: "SUCCESS", false: "FAILURE"}[*fin.Passed]
	}
	return end, true
}

// prowJobEnd reads the end of a run whose pod ended without uploading
// finished.json, e.g. when it was aborted, from prowjob.json.
func prowJobEnd(pj prowJobSpec) (runEnd, bool) {
	if !terminalStates[pj.Status.State] {
		return runEnd{}, false
	}
	return runEnd{Result: strings.ToUpper(pj.Status.State), Finished: pj.Status.CompletionTime}, true
}

// parseStartedRepos reads the refs of the repository that holds the PR from
// started.json's repos, whose values look like "main:<base_sha>,<pr>:<pr_sha>".
func parseStartedRepos(repos map[string]string, pr string) (baseRef, baseSHA, prSHA string) {
	for _, refs := range repos {
		parts := strings.Split(refs, ",")
		for _, part := range parts[1:] {
			if num, sha, _ := strings.Cut(part, ":"); num == pr {
				baseRef, baseSHA, _ = strings.Cut(parts[0], ":")
				return baseRef, baseSHA, sha
			}
		}
	}
	return "", "", ""
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
	Name string `json:"name"`
	// Step is the multi-stage step name, the step parameter of
	// get_prow_artifact_content.
	Step            string  `json:"step,omitempty"`
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
	// Repos maps org/repo to its refs, as "main:<base_sha>,<pr>:<pr_sha>".
	Repos map[string]string `json:"repos"`
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
var nonTestDirs = map[string]bool{"build-logs": true, "build-resources": true, "release": true, "junit": true, "job_labels": true}

// testDirs returns the test directories in a listing of artifacts/.
func testDirs(entries []client.ArtifactEntry) []string {
	var dirs []string
	for _, e := range entries {
		if e.Type == "dir" && e.Name != ".." && !nonTestDirs[e.Name] {
			dirs = append(dirs, e.Name)
		}
	}
	return dirs
}

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
			return tools.ToolError(err)
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

	if isNotFound(startedErr) && isNotFound(finishedErr) && isNotFound(prowjobErr) {
		return run, &client.HTTPError{StatusCode: http.StatusNotFound, Body: []byte(fmt.Sprintf("run %s/%s not found (it may not exist, or it has not started uploading yet)", ref.Job, ref.BuildID))}
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
	end, ended := finishedEnd(finished, finishedErr)

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
		if !ended {
			if end, ended = prowJobEnd(pj); ended {
				run.Notes = append(run.Notes, "finished.json is missing; result taken from prowjob.json")
			}
		}
	}
	if ended {
		run.State = "finished"
		run.Result = end.Result
		if !end.Finished.IsZero() {
			run.Finished = end.Finished.UTC().Format(time.RFC3339)
		}
		run.DurationSeconds = end.duration(st)
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
		run.TestDirs = testDirs(listing.Entries)
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
			step := prowStep{Name: c.Name, Step: stepSlug(c.Name), Passed: c.Failure == nil, DurationSeconds: c.Time}
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

// multiStageStep prefixes the junit name of a ci-operator multi-stage step.
const multiStageStep = "Run multi-stage step "

// stepSlug returns the step name of a multi-stage step junit case, or "".
func stepSlug(name string) string {
	if slug, ok := strings.CutPrefix(name, multiStageStep); ok && plainName(slug) {
		return slug
	}
	return ""
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
		if errors.Is(err, client.ErrIsFile) {
			return tools.InvalidParam("path", strings.TrimSuffix(rel, "/")+" is a file; use get_prow_artifact_content")
		}
		if err != nil {
			return tools.ToolError(err)
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

var buildID = regexp.MustCompile(`^\d+$`)

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
		// Accept the junit name that get_prow_job_run reports as well.
		step := strings.TrimPrefix(strings.TrimSpace(req.GetString("step", "")), multiStageStep)
		switch {
		case rel != "" && step != "":
			return tools.InvalidParam("step", "give path or step, not both")
		case step != "":
			test := req.GetString("test", "")
			if !plainName(step) || (test != "" && !plainName(test)) {
				return tools.InvalidParam("step", "step and test must be plain names")
			}
			if test == "" {
				var errRes *mcp.CallToolResult
				if test, errRes = onlyTestDir(ctx, prow, ref); errRes != nil {
					return errRes, nil
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
		if errors.Is(err, client.ErrIsDirectory) {
			return tools.InvalidParam("path", rel+" is a directory; use get_prow_artifacts")
		}
		if err != nil {
			if isNotFound(err) {
				return tools.ToolError(&client.HTTPError{StatusCode: http.StatusNotFound, Body: []byte(fmt.Sprintf(
					"%s not found in run %s/%s (it may not exist, or the step has not finished yet)", rel, ref.Job, ref.BuildID))})
			}
			return tools.ToolError(err)
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
// is exactly one, or the tool result that explains why there is not.
func onlyTestDir(ctx context.Context, prow client.Prow, ref client.RunRef) (string, *mcp.CallToolResult) {
	notFound := func(format string) (string, *mcp.CallToolResult) {
		res, _ := tools.ToolError(&client.HTTPError{StatusCode: http.StatusNotFound, Body: []byte(fmt.Sprintf(format, ref.Job, ref.BuildID))})
		return "", res
	}
	listing, err := prow.List(ctx, ref.Deck, ref.Path+"/artifacts/")
	if isNotFound(err) {
		return notFound("run %s/%s has no artifacts/ yet (it may not exist, or no step has finished)")
	}
	if err != nil {
		res, _ := tools.ToolError(fmt.Errorf("listing artifacts/ to find the test: %w", err))
		return "", res
	}
	switch dirs := testDirs(listing.Entries); len(dirs) {
	case 1:
		return dirs[0], nil
	case 0:
		return notFound("run %s/%s has no test directory under artifacts/ (it ended before any test step ran, or none has uploaded yet); read build-log.txt instead")
	default:
		res, _ := tools.InvalidParam("test", "set test to one of: "+strings.Join(dirs, ", "))
		return "", res
	}
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return tools.ToolError(err)
	}
	return mcp.NewToolResultText(string(data)), nil
}
