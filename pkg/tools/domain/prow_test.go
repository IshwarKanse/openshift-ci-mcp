package domain_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openshift-eng/openshift-ci-mcp/pkg/client"
	"github.com/openshift-eng/openshift-ci-mcp/pkg/tools/domain"
)

var testDeck = client.Deck{
	Name:      "public",
	DeckURL:   "https://deck.example",
	GCSWebURL: "https://gcsweb.example",
	Buckets:   []string{"bkt"},
}

// fakeProw is safe for concurrent use: get_prow_job_run reads several
// objects in parallel.
type fakeProw struct {
	mu         sync.Mutex
	history    map[string]*client.JobHistoryPage // keyed by start build ID
	objects    map[string]string                 // keyed by object path
	types      map[string]string                 // content types by object path
	listings   map[string][]client.ArtifactEntry // keyed by directory path
	truncated  bool
	failing    map[string]bool // paths that return a 502
	opened     []string
	historyReq []string
}

func (f *fakeProw) Decks() []client.Deck { return []client.Deck{testDeck} }

func (f *fakeProw) JobHistory(_ context.Context, _ client.Deck, dir, job, start string) (*client.JobHistoryPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.historyReq = append(f.historyReq, dir+"|"+job+"|"+start)
	if p, ok := f.history[start]; ok {
		return p, nil
	}
	return nil, &client.HTTPError{StatusCode: http.StatusNotFound, Body: []byte("not found")}
}

func (f *fakeProw) List(_ context.Context, _ client.Deck, dir string) (*client.Listing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dir = strings.TrimSuffix(dir, "/") + "/"
	if l, ok := f.listings[dir]; ok {
		return &client.Listing{Entries: l, Truncated: f.truncated}, nil
	}
	return nil, &client.HTTPError{StatusCode: http.StatusNotFound, Body: []byte("not found")}
}

func (f *fakeProw) Open(_ context.Context, _ client.Deck, path string) (*client.Object, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, path)
	if f.failing[path] {
		return nil, &client.HTTPError{StatusCode: http.StatusBadGateway, Body: []byte("bad gateway")}
	}
	if body, ok := f.objects[path]; ok {
		ct := f.types[path]
		if ct == "" {
			ct = "text/plain; charset=utf-8"
		}
		return &client.Object{Body: io.NopCloser(strings.NewReader(body)), ContentType: ct}, nil
	}
	return nil, &client.HTTPError{StatusCode: http.StatusNotFound, Body: []byte("not found")}
}

func callTool(t *testing.T, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) (map[string]any, bool) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	text := res.Content[0].(mcp.TextContent).Text
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		if !res.IsError {
			t.Fatalf("result is not JSON: %v: %s", err, text)
		}
		out = map[string]any{"error": text}
	}
	return out, res.IsError
}

func build(id string, pr int, result string) client.JobHistoryBuild {
	b := client.JobHistoryBuild{
		SpyglassLink: "/view/gs/bkt/pr-logs/pull/o_r/1/job/" + id,
		ID:           id,
		Started:      time.Date(2026, 10, 23, 4, 0, 0, 0, time.UTC),
		Duration:     90 * time.Second,
		Result:       result,
	}
	if pr != 0 {
		b.Refs = &client.JobRefs{Pulls: []client.JobPull{{Number: pr, SHA: "sha" + id}}}
	}
	return b
}

func historyFake() *fakeProw {
	return &fakeProw{history: map[string]*client.JobHistoryPage{
		"":   {Builds: []client.JobHistoryBuild{build("9", 1, "PENDING"), build("8", 2, "SUCCESS"), build("7", 1, "ABORTED")}, OlderBuildID: "6"},
		"6":  {Builds: []client.JobHistoryBuild{build("6", 2, "FAILURE"), build("5", 1, "SUCCESS")}, OlderBuildID: ""},
		"40": {Builds: []client.JobHistoryBuild{build("40", 1, "SUCCESS")}},
	}}
}

func runsOf(t *testing.T, out map[string]any) []string {
	t.Helper()
	var ids []string
	for _, r := range out["runs"].([]any) {
		ids = append(ids, r.(map[string]any)["build_id"].(string))
	}
	return ids
}

func TestGetProwJobHistory(t *testing.T) {
	f := historyFake()
	out, isErr := callTool(t, domain.GetProwJobHistoryHandler(f, nil), map[string]any{"job_name": "pull-ci-o-r-main-e2e", "limit": float64(4)})
	if isErr {
		t.Fatalf("unexpected error: %v", out)
	}
	if got := strings.Join(runsOf(t, out), ","); got != "9,8,7,6" {
		t.Errorf("runs = %s, want 9,8,7,6", got)
	}
	if out["next_start_build_id"] != "5" {
		t.Errorf("next_start_build_id = %v, want 5", out["next_start_build_id"])
	}
	first := out["runs"].([]any)[0].(map[string]any)
	if first["result"] != "PENDING" || first["pr"] != float64(1) || first["duration_seconds"] != float64(90) ||
		first["url"] != "https://deck.example/view/gs/bkt/pr-logs/pull/o_r/1/job/9" || first["started"] != "2026-10-23T04:00:00Z" {
		t.Errorf("unexpected run: %v", first)
	}
	if f.historyReq[0] != "pr-logs/directory|pull-ci-o-r-main-e2e|" {
		t.Errorf("history request = %q", f.historyReq[0])
	}
}

func TestGetProwJobHistory_PRFilterAcrossPages(t *testing.T) {
	out, isErr := callTool(t, domain.GetProwJobHistoryHandler(historyFake(), nil), map[string]any{"job_name": "rehearse-1-pull-ci-x", "pr": "1"})
	if isErr {
		t.Fatalf("unexpected error: %v", out)
	}
	if got := strings.Join(runsOf(t, out), ","); got != "9,7,5" {
		t.Errorf("runs = %s, want 9,7,5", got)
	}
	if _, ok := out["next_start_build_id"]; ok {
		t.Errorf("no more pages expected, got next_start_build_id %v", out["next_start_build_id"])
	}
	if out["pages_scanned"] != float64(2) {
		t.Errorf("pages_scanned = %v, want 2", out["pages_scanned"])
	}
}

func TestGetProwJobHistory_LimitAtPageEnd(t *testing.T) {
	out, _ := callTool(t, domain.GetProwJobHistoryHandler(historyFake(), nil), map[string]any{"job_name": "periodic-x", "limit": float64(3)})
	if got := strings.Join(runsOf(t, out), ","); got != "9,8,7" {
		t.Errorf("runs = %s", got)
	}
	if out["next_start_build_id"] != "6" {
		t.Errorf("next_start_build_id = %v, want 6", out["next_start_build_id"])
	}
}

func TestGetProwJobHistory_StartAndJobType(t *testing.T) {
	f := historyFake()
	out, _ := callTool(t, domain.GetProwJobHistoryHandler(f, nil), map[string]any{"job_name": "pull-ci-x", "job_type": "periodic", "start_build_id": "40"})
	if got := strings.Join(runsOf(t, out), ","); got != "40" {
		t.Errorf("runs = %s", got)
	}
	if f.historyReq[0] != "logs|pull-ci-x|40" {
		t.Errorf("history request = %q", f.historyReq[0])
	}
}

func TestGetProwJobHistory_InvalidParams(t *testing.T) {
	h := domain.GetProwJobHistoryHandler(historyFake(), nil)
	for name, args := range map[string]map[string]any{
		"missing job":  {},
		"unknown deck": {"job_name": "j", "deck": "nope"},
		"bad job type": {"job_name": "j", "job_type": "batch"},
		"bad pr":       {"job_name": "j", "pr": "abc"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, isErr := callTool(t, h, args); !isErr {
				t.Error("expected a tool error")
			}
		})
	}
}

const runPath = "logs/periodic-x/123"

const junitXML = `<testsuites><testsuite name="step graph">
<testcase name="" time="0"></testcase>
<testcase name="Build image foo from the repository" time="12.5"></testcase>
<testcase name="Run multi-stage step install" time="300"><failure message="">step install failed: exit 1 boom</failure></testcase>
<testcase name="Run multi-stage step skipped-step" time="0"><skipped/></testcase>
</testsuite></testsuites>`

func runFake() *fakeProw {
	return &fakeProw{
		objects: map[string]string{
			runPath + "/started.json":                        `{"timestamp":1792731000}`,
			runPath + "/finished.json":                       `{"timestamp":1792731600,"passed":false,"result":"FAILURE","revision":"abc"}`,
			runPath + "/prowjob.json":                        `{"spec":{"type":"periodic","job":"periodic-x","refs":{"org":"o","repo":"r","base_ref":"main","base_sha":"abc","pulls":[{"number":5,"sha":"def","title":"T"}]}},"status":{"state":"failure"}}`,
			runPath + "/artifacts/junit_operator.xml":        junitXML,
			runPath + "/build-log.txt":                       "l1\nl2\nl3\nl4\nl5\n",
			runPath + "/artifacts/e2e/install/build-log.txt": "a\nerror one\nb\nc\nd\ne\nerror two\nf\n",
			runPath + "/artifacts/screenshot.png":            "\x89PNG\x00\x00",
			runPath + "/artifacts/odd.bin":                   "abc\x00def",
		},
		types: map[string]string{runPath + "/artifacts/screenshot.png": "image/png"},
		listings: map[string][]client.ArtifactEntry{
			runPath + "/artifacts/": {
				{Name: "..", Type: "dir", Path: "logs/periodic-x/"},
				{Name: "build-logs", Type: "dir", Path: runPath + "/artifacts/build-logs/"},
				{Name: "e2e", Type: "dir", Path: runPath + "/artifacts/e2e/"},
				{Name: "junit_operator.xml", Type: "file", Size: 300, Path: runPath + "/artifacts/junit_operator.xml"},
			},
		},
	}
}

const runURL = "https://deck.example/view/gs/bkt/" + runPath

func TestGetProwJobRun(t *testing.T) {
	out, isErr := callTool(t, domain.GetProwJobRunHandler(runFake(), nil), map[string]any{"run_url": runURL})
	if isErr {
		t.Fatalf("unexpected error: %v", out)
	}
	for k, want := range map[string]any{
		"job": "periodic-x", "build_id": "123", "state": "finished", "result": "FAILURE",
		"duration_seconds": float64(600), "job_type": "periodic", "started": "2026-10-23T04:50:00Z",
		"url": "https://deck.example/view/gs/bkt/" + runPath,
	} {
		if out[k] != want {
			t.Errorf("%s = %v, want %v", k, out[k], want)
		}
	}
	steps := out["steps"].([]any)
	if len(steps) != 2 {
		t.Fatalf("got %d steps, want 2 (empty and skipped cases dropped): %v", len(steps), steps)
	}
	failed := steps[0].(map[string]any)
	if failed["name"] != "Run multi-stage step install" || failed["passed"] != false || !strings.Contains(failed["failure"].(string), "step install failed") {
		t.Errorf("failed step not first or wrong: %v", failed)
	}
	if got := out["failed_steps"].([]any); len(got) != 1 {
		t.Errorf("failed_steps = %v", got)
	}
	if got := out["test_dirs"].([]any); len(got) != 1 || got[0] != "e2e" {
		t.Errorf("test_dirs = %v, want [e2e]", got)
	}
	refs := out["refs"].(map[string]any)
	if refs["pr"] != float64(5) || refs["pr_title"] != "T" {
		t.Errorf("refs = %v", refs)
	}
}

func TestGetProwJobRun_SingleTestsuiteRoot(t *testing.T) {
	f := runFake()
	f.objects[runPath+"/artifacts/junit_operator.xml"] = `<testsuite name="s"><testcase name="Run multi-stage step a" time="1"><failure>x</failure></testcase></testsuite>`
	out, _ := callTool(t, domain.GetProwJobRunHandler(f, nil), map[string]any{"run_url": runURL})
	if steps, _ := out["steps"].([]any); len(steps) != 1 {
		t.Errorf("expected 1 step from a <testsuite> root, got %v", out["steps"])
	}
}

func TestGetProwJobRun_TransientErrorNotCached(t *testing.T) {
	f := runFake()
	f.failing = map[string]bool{runPath + "/artifacts/junit_operator.xml": true}
	h := domain.GetProwJobRunHandler(f, client.NewResponseCache(time.Minute))
	if out, isErr := callTool(t, h, map[string]any{"run_url": runURL}); !isErr {
		t.Fatalf("expected a tool error for a transient junit failure, got %v", out)
	}
	f.failing = nil
	out, isErr := callTool(t, h, map[string]any{"run_url": runURL})
	if isErr || len(out["steps"].([]any)) != 2 {
		t.Errorf("retry should read the steps, got %v", out)
	}
}

func TestGetProwJobRun_RunningIsNotCached(t *testing.T) {
	f := runFake()
	delete(f.objects, runPath+"/finished.json")
	f.objects[runPath+"/prowjob.json"] = `{"spec":{"type":"periodic","job":"periodic-x"},"status":{"state":"pending"}}`
	cache := client.NewResponseCache(time.Minute)
	h := domain.GetProwJobRunHandler(f, cache)
	out, isErr := callTool(t, h, map[string]any{"run_url": runURL})
	if isErr || out["state"] != "running" {
		t.Fatalf("expected a running job, got %v", out)
	}
	f.objects[runPath+"/finished.json"] = `{"timestamp":1792731600,"result":"SUCCESS"}`
	out, _ = callTool(t, h, map[string]any{"run_url": runURL})
	if out["state"] != "finished" || out["result"] != "SUCCESS" {
		t.Errorf("second call should see the finished run, got %v", out)
	}
}

func TestGetProwJobRun_AbortedWithoutFinishedJSON(t *testing.T) {
	f := runFake()
	delete(f.objects, runPath+"/finished.json")
	f.objects[runPath+"/prowjob.json"] = `{"spec":{"type":"periodic","job":"periodic-x"},"status":{"state":"aborted","completionTime":"2026-10-23T05:00:00Z"}}`
	out, isErr := callTool(t, domain.GetProwJobRunHandler(f, nil), map[string]any{"run_url": runURL})
	if isErr {
		t.Fatalf("unexpected error: %v", out)
	}
	if out["state"] != "finished" || out["result"] != "ABORTED" || out["finished"] != "2026-10-23T05:00:00Z" || out["duration_seconds"] != float64(600) {
		t.Errorf("unexpected run: %v", out)
	}
}

func TestGetProwJobHistory_NewestPageNotCached(t *testing.T) {
	f := historyFake()
	cache := client.NewResponseCache(time.Minute)
	h := domain.GetProwJobHistoryHandler(f, cache)
	args := map[string]any{"job_name": "periodic-x", "limit": float64(1)}
	callTool(t, h, args)
	f.history[""].Builds[0].Result = "SUCCESS"
	out, _ := callTool(t, h, args)
	if r := out["runs"].([]any)[0].(map[string]any)["result"]; r != "SUCCESS" {
		t.Errorf("newest page served from cache: result %v", r)
	}
	callTool(t, h, map[string]any{"job_name": "periodic-x", "start_build_id": "40"})
	callTool(t, h, map[string]any{"job_name": "periodic-x", "start_build_id": "40"})
	if n := strings.Count(strings.Join(f.historyReq, "\n"), "|40"); n != 1 {
		t.Errorf("older page fetched %d times, want 1 (cached)", n)
	}
}

func TestGetProwJobRun_NotFound(t *testing.T) {
	_, isErr := callTool(t, domain.GetProwJobRunHandler(&fakeProw{}, nil), map[string]any{"run_url": runURL})
	if !isErr {
		t.Error("expected a tool error for a missing run")
	}
}

func TestGetProwJobRun_JobNameAndBuildID(t *testing.T) {
	out, isErr := callTool(t, domain.GetProwJobRunHandler(runFake(), nil), map[string]any{"job_name": "periodic-x", "build_id": "123"})
	if isErr || out["result"] != "FAILURE" {
		t.Errorf("unexpected result: %v", out)
	}
	for name, args := range map[string]map[string]any{
		"nothing":          {},
		"no build id":      {"job_name": "periodic-x"},
		"foreign host":     {"run_url": "https://evil.example/view/gs/bkt/" + runPath},
		"presubmit, no pr": {"job_name": "pull-ci-x", "build_id": "1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, isErr := callTool(t, domain.GetProwJobRunHandler(runFake(), nil), args); !isErr {
				t.Error("expected a tool error")
			}
		})
	}
}

func TestGetProwArtifacts(t *testing.T) {
	out, isErr := callTool(t, domain.GetProwArtifactsHandler(runFake()), map[string]any{"run_url": runURL})
	if isErr {
		t.Fatalf("unexpected error: %v", out)
	}
	entries := out["entries"].([]any)
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3 (parent link dropped): %v", len(entries), entries)
	}
	if p := entries[1].(map[string]any)["path"]; p != "artifacts/e2e/" {
		t.Errorf("path not relative to the run: %v", p)
	}
	if _, ok := out["truncated"]; ok {
		t.Error("truncated should be omitted for a complete listing")
	}
	f := runFake()
	f.truncated = true
	if out, _ := callTool(t, domain.GetProwArtifactsHandler(f), map[string]any{"run_url": runURL}); out["truncated"] != true {
		t.Errorf("expected truncated=true, got %v", out["truncated"])
	}
	if _, isErr := callTool(t, domain.GetProwArtifactsHandler(runFake()), map[string]any{"run_url": runURL, "path": "../other"}); !isErr {
		t.Error("expected path traversal to be rejected")
	}
}

func TestGetProwArtifactContent(t *testing.T) {
	h := domain.GetProwArtifactContentHandler(runFake())
	tests := []struct {
		name     string
		args     map[string]any
		wantText string
		wantPath string
		wantMode string
	}{
		{"default tail of build log", map[string]any{"tail_lines": float64(2)}, "l4\nl5", "build-log.txt", "tail"},
		{"head", map[string]any{"head_lines": float64(2)}, "l1\nl2", "build-log.txt", "head"},
		{"step shorthand with grep", map[string]any{"step": "install", "grep": "error", "context_lines": float64(1)}, "a\nerror one\nb\n--\ne\nerror two\nf", "artifacts/e2e/install/build-log.txt", "grep"},
		{"explicit path", map[string]any{"path": "artifacts/e2e/install/build-log.txt", "grep": "two", "context_lines": float64(0)}, "error two", "artifacts/e2e/install/build-log.txt", "grep"},
		{"binary by content type", map[string]any{"path": "artifacts/screenshot.png"}, "binary content (image/png); open the URL instead", "artifacts/screenshot.png", "binary"},
		{"binary by NUL byte", map[string]any{"path": "artifacts/odd.bin"}, "", "artifacts/odd.bin", "binary"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{"run_url": runURL}
			for k, v := range tt.args {
				args[k] = v
			}
			out, isErr := callTool(t, h, args)
			if isErr {
				t.Fatalf("unexpected error: %v", out)
			}
			if out["mode"] != tt.wantMode || out["path"] != tt.wantPath {
				t.Errorf("mode/path = %v/%v, want %v/%v", out["mode"], out["path"], tt.wantMode, tt.wantPath)
			}
			if tt.wantText != "" && out["text"] != tt.wantText {
				t.Errorf("text = %q, want %q", out["text"], tt.wantText)
			}
		})
	}
}

func TestGetProwArtifactContent_Errors(t *testing.T) {
	h := domain.GetProwArtifactContentHandler(runFake())
	for name, args := range map[string]map[string]any{
		"missing file":  {"path": "artifacts/nope.txt"},
		"traversal":     {"path": "artifacts/../../x/build-log.txt"},
		"path and step": {"path": "build-log.txt", "step": "install"},
		"bad step name": {"step": "../x"},
		"dot-dot step":  {"step": ".."},
		"dot test":      {"step": "install", "test": "."},
		"bad regexp":    {"grep": "("},
		"directory":     {"path": "artifacts/"},
		"no test dir":   {"step": "install", "test": "missing"},
	} {
		t.Run(name, func(t *testing.T) {
			args["run_url"] = runURL
			if out, isErr := callTool(t, h, args); !isErr {
				t.Errorf("expected a tool error, got %v", out)
			}
		})
	}
}

func TestGetProwArtifactContent_MissingFileMessage(t *testing.T) {
	out, _ := callTool(t, domain.GetProwArtifactContentHandler(runFake()), map[string]any{"run_url": runURL, "step": "later", "test": "e2e"})
	if !strings.Contains(out["error"].(string), "has not finished yet") {
		t.Errorf("unexpected error message: %v", out["error"])
	}
}

func TestGetProwArtifactContent_AmbiguousTest(t *testing.T) {
	f := runFake()
	l := f.listings[runPath+"/artifacts/"]
	f.listings[runPath+"/artifacts/"] = append(l, client.ArtifactEntry{Name: "e2e-two", Type: "dir"})
	out, isErr := callTool(t, domain.GetProwArtifactContentHandler(f), map[string]any{"run_url": runURL, "step": "install"})
	if !isErr || !strings.Contains(out["error"].(string), "e2e, e2e-two") {
		t.Errorf("expected an error listing the test dirs, got %v", out)
	}
}
