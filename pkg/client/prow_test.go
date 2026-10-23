package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const jobHistoryPage = `<html><body>
<a href="/job-history/gs/test-platform-results-public/logs/my-job?buildId=300">Newer Runs</a>
<a href="/job-history/gs/test-platform-results-public/logs/my-job?buildId=150">Older Runs</a>
<script type="text/javascript">
var allBuilds = [{"SpyglassLink":"/view/gs/b/logs/my-job/200","ID":"200","Started":"2026-10-23T04:32:50Z","Duration":460000000000,"Result":"SUCCESS","Refs":{"org":"o","repo":"r","base_ref":"main","base_sha":"abc","pulls":[{"number":7,"author":"a","sha":"def"}]}},{"SpyglassLink":"/view/gs/b/logs/my-job/160","ID":"160","Started":"2026-10-22T04:32:50Z","Duration":0,"Result":"ABORTED","Refs":null}];
</script></body></html>`

func TestParseJobHistory(t *testing.T) {
	page, err := ParseJobHistory([]byte(jobHistoryPage))
	if err != nil {
		t.Fatalf("ParseJobHistory: %v", err)
	}
	if len(page.Builds) != 2 {
		t.Fatalf("got %d builds, want 2", len(page.Builds))
	}
	b := page.Builds[0]
	if b.ID != "200" || b.Result != "SUCCESS" || b.Duration.Seconds() != 460 {
		t.Errorf("unexpected first build: %+v", b)
	}
	if b.Refs == nil || len(b.Refs.Pulls) != 1 || b.Refs.Pulls[0].Number != 7 {
		t.Errorf("unexpected refs: %+v", b.Refs)
	}
	if page.Builds[1].Refs != nil {
		t.Errorf("expected nil refs for the second build")
	}
	if page.OlderBuildID != "150" {
		t.Errorf("OlderBuildID = %q, want 150", page.OlderBuildID)
	}
}

func TestParseJobHistory_NoBuildList(t *testing.T) {
	if _, err := ParseJobHistory([]byte("<html>login</html>")); err == nil {
		t.Fatal("expected an error for a page without allBuilds")
	}
}

func TestParseJobHistory_Empty(t *testing.T) {
	page, err := ParseJobHistory([]byte("var allBuilds = [];\n"))
	if err != nil {
		t.Fatalf("ParseJobHistory: %v", err)
	}
	if len(page.Builds) != 0 || page.OlderBuildID != "" {
		t.Errorf("unexpected page: %+v", page)
	}
}

func listingPage(bucket, dir string, rows ...string) string {
	var b strings.Builder
	b.WriteString(`<html><body><ul class="resource-grid">`)
	b.WriteString(`<li class="pure-g grid-row"><div class="pure-u-2-5"><a href="/gcs/` + bucket + `/` + parentOf(dir) + `"><img src="/icons/back.png"> ..</a></div><div class="pure-u-1-5">-</div><div class="pure-u-2-5">-</div></li>`)
	for _, r := range rows {
		b.WriteString(r)
	}
	b.WriteString(`</ul></body></html>`)
	return b.String()
}

func parentOf(dir string) string {
	dir = strings.TrimSuffix(dir, "/")
	if i := strings.LastIndex(dir, "/"); i >= 0 {
		return dir[:i+1]
	}
	return ""
}

func nextButtonHTML(path, marker string) string {
	return `
    <a href="/gcs/bkt/` + path + `?marker=` + marker + `"
	   class="pure-button next-button">
	   Next page
	</a>`
}

func listingRowHTML(bucket, path, kind, name, size, modified string) string {
	return `<li class="pure-g grid-row">
	    <div class="pure-u-2-5"><a href="/gcs/` + bucket + `/` + path + `"><img src="/icons/` + kind + `.png"> ` + name + `</a></div>
	    <div class="pure-u-1-5">` + size + `</div>
	    <div class="pure-u-2-5">` + modified + `</div>
	</li>`
}

func TestParseGCSWebListing(t *testing.T) {
	page := listingPage("bkt", "logs/j/1/artifacts/",
		listingRowHTML("bkt", "logs/j/1/artifacts/e2e/", "dir", "e2e/", "-", "-"),
		listingRowHTML("bkt", "logs/j/1/artifacts/junit_operator.xml", "file", "junit_operator.xml", "572", "Thu, 22 Oct 2026 12:43:18 UTC"),
	)
	entries, next, err := ParseGCSWebListing([]byte(page), "bkt")
	if err != nil {
		t.Fatalf("ParseGCSWebListing: %v", err)
	}
	if next != "" {
		t.Errorf("next = %q, want empty", next)
	}
	// The ".." back link uses back.png and is not matched.
	if len(entries) != 2 {
		t.Fatalf("got %d entries %+v, want 2", len(entries), entries)
	}
	if e := entries[0]; e.Name != "e2e" || e.Type != "dir" || e.Path != "logs/j/1/artifacts/e2e/" || e.Size != 0 || e.Modified != "" {
		t.Errorf("unexpected dir entry: %+v", e)
	}
	if e := entries[1]; e.Name != "junit_operator.xml" || e.Type != "file" || e.Size != 572 || e.Modified == "" {
		t.Errorf("unexpected file entry: %+v", e)
	}
}

func TestParseGCSWebListing_NotAListing(t *testing.T) {
	if _, _, err := ParseGCSWebListing([]byte("<html>sign in</html>"), "bkt"); err == nil {
		t.Fatal("expected an error for a non-listing page")
	}
}

func TestParseRunURL(t *testing.T) {
	tests := []struct {
		name, url                string
		wantPath, wantJob, wantB string
		wantPR, wantOrgRepo      string
	}{
		{
			name:     "spyglass presubmit",
			url:      "https://prow.ci.openshift.org/view/gs/test-platform-results-public/pr-logs/pull/openshift_release/87540/pull-ci-openshift-release-main-core-valid/2113247331204730880",
			wantPath: "pr-logs/pull/openshift_release/87540/pull-ci-openshift-release-main-core-valid/2113247331204730880",
			wantJob:  "pull-ci-openshift-release-main-core-valid", wantB: "2113247331204730880",
			wantPR: "87540", wantOrgRepo: "openshift_release",
		},
		{
			name:     "gcsweb file inside a periodic run",
			url:      "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/test-platform-results-public/logs/periodic-x/123/artifacts/e2e/step/build-log.txt",
			wantPath: "logs/periodic-x/123", wantJob: "periodic-x", wantB: "123",
		},
		{
			name:     "old bucket name",
			url:      "https://prow.ci.openshift.org/view/gs/test-platform-results/logs/branch-ci-x/456",
			wantPath: "logs/branch-ci-x/456", wantJob: "branch-ci-x", wantB: "456",
		},
		{
			name:     "redirect host with trailing slash",
			url:      "https://gcs.ci.openshift.org/gcs/test-platform-results-public/logs/periodic-x/123/",
			wantPath: "logs/periodic-x/123", wantJob: "periodic-x", wantB: "123",
		},
		{
			name:     "storage.googleapis.com object URL",
			url:      "https://storage.googleapis.com/test-platform-results-public/logs/periodic-x/123/finished.json",
			wantPath: "logs/periodic-x/123", wantJob: "periodic-x", wantB: "123",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := ParseRunURL(DefaultDecks, tt.url)
			if err != nil {
				t.Fatalf("ParseRunURL: %v", err)
			}
			if ref.Path != tt.wantPath || ref.Job != tt.wantJob || ref.BuildID != tt.wantB || ref.PR != tt.wantPR || ref.OrgRepo != tt.wantOrgRepo {
				t.Errorf("got %+v", ref)
			}
			if ref.Deck.Name != "public" {
				t.Errorf("deck = %q, want public", ref.Deck.Name)
			}
		})
	}
}

func TestParseRunURL_Buckets(t *testing.T) {
	ref, err := ParseRunURL(DefaultDecks, "https://prow.ci.openshift.org/view/gs/test-platform-results/logs/j/1")
	if err != nil || ref.Deck.Bucket() != "test-platform-results-public" {
		t.Errorf("alias should read the main bucket, got %q (%v)", ref.Deck.Bucket(), err)
	}
	multi := []Deck{{Name: "m", DeckURL: "https://deck.example", GCSWebURL: "https://gcsweb.example", Buckets: []string{"first", "second"}}}
	ref, err = ParseRunURL(multi, "https://deck.example/view/gs/second/logs/j/1")
	if err != nil || ref.Deck.Bucket() != "second" {
		t.Errorf("second bucket not kept, got %q (%v)", ref.Deck.Bucket(), err)
	}
	if !strings.Contains(ref.ObjectURL("x"), "/gcs/second/") || !strings.Contains(ref.SpyglassURL(), "/gs/second/") {
		t.Errorf("URLs use the wrong bucket: %s %s", ref.ObjectURL("x"), ref.SpyglassURL())
	}
	if multi[0].Bucket() != "first" {
		t.Error("ParseRunURL modified the deck list")
	}
}

func TestParseRunURL_Rejects(t *testing.T) {
	for name, u := range map[string]string{
		"unknown host":    "https://evil.example.com/view/gs/test-platform-results-public/logs/j/1",
		"unknown bucket":  "https://prow.ci.openshift.org/view/gs/other-bucket/logs/j/1",
		"not a run path":  "https://prow.ci.openshift.org/view/gs/test-platform-results-public/logs/j",
		"not a URL":       "logs/j/1",
		"non-numeric id":  "https://prow.ci.openshift.org/view/gs/test-platform-results-public/logs/j/latest",
		"path traversal":  "https://prow.ci.openshift.org/view/gs/test-platform-results-public/logs/../x/1",
		"dot-dot job":     "https://prow.ci.openshift.org/view/gs/test-platform-results-public/logs/../1",
		"dot-dot repo":    "https://prow.ci.openshift.org/view/gs/test-platform-results-public/pr-logs/pull/../1/j/2",
		"job history URL": "https://prow.ci.openshift.org/job-history/gs/test-platform-results-public/logs/j",
	} {
		t.Run(name, func(t *testing.T) {
			if ref, err := ParseRunURL(DefaultDecks, u); err == nil {
				t.Errorf("expected an error, got %+v", ref)
			}
		})
	}
}

func TestNewRunRef(t *testing.T) {
	d := DefaultDecks[0]
	tests := []struct {
		name, job, build, pr, orgRepo string
		wantPath                      string
		wantErr                       bool
	}{
		{name: "periodic", job: "periodic-x", build: "1", wantPath: "logs/periodic-x/1"},
		{name: "rehearsal derives pr and repo", job: "rehearse-87540-pull-ci-x", build: "2", wantPath: "pr-logs/pull/openshift_release/87540/rehearse-87540-pull-ci-x/2"},
		{name: "presubmit with org/repo", job: "pull-ci-o-r-main-e2e", build: "3", pr: "12", orgRepo: "o/r", wantPath: "pr-logs/pull/o_r/12/pull-ci-o-r-main-e2e/3"},
		{name: "presubmit without pr", job: "pull-ci-o-r-main-e2e", build: "3", wantErr: true},
		{name: "presubmit without repo", job: "pull-ci-o-r-main-e2e", build: "3", pr: "12", wantErr: true},
		{name: "non-numeric build", job: "periodic-x", build: "latest", wantErr: true},
		{name: "non-numeric pr", job: "pull-ci-x", build: "1", pr: "abc", orgRepo: "o_r", wantErr: true},
		{name: "slash in job", job: "../x", build: "1", wantErr: true},
		{name: "dot-dot job", job: "..", build: "1", wantErr: true},
		{name: "dot-dot repo", job: "pull-ci-x", build: "1", pr: "2", orgRepo: "..", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := NewRunRef(d, tt.job, tt.build, tt.pr, tt.orgRepo)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && ref.Path != tt.wantPath {
				t.Errorf("Path = %q, want %q", ref.Path, tt.wantPath)
			}
		})
	}
}

func TestDeckByName(t *testing.T) {
	if _, err := DeckByName(DefaultDecks, "public"); err != nil {
		t.Errorf("public: %v", err)
	}
	if _, err := DeckByName(DefaultDecks, "nope"); err == nil {
		t.Error("expected an error for an unknown deck")
	}
}

func testDeck(srv *httptest.Server) Deck {
	return Deck{Name: "test", DeckURL: srv.URL, GCSWebURL: srv.URL, Buckets: []string{"bkt"}}
}

func TestProwClient_RedirectOutsideDeckRefused(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "elsewhere")
	}))
	defer other.Close()
	otherURL := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, otherURL+r.URL.Path, http.StatusFound)
	}))
	defer srv.Close()
	d := testDeck(srv)
	c := NewProw([]Deck{d}, srv.Client())
	if _, err := c.Open(context.Background(), d, "logs/j/1/build-log.txt"); err == nil || !strings.Contains(err.Error(), "outside deck") {
		t.Fatalf("expected the redirect to be refused, got %v", err)
	}
	d.ExtraHosts = []string{strings.TrimPrefix(otherURL, "http://")}
	obj, err := c.Open(context.Background(), d, "logs/j/1/build-log.txt")
	if err != nil {
		t.Fatalf("redirect to an extra host of the deck should work: %v", err)
	}
	obj.Body.Close()
}

func TestProwClient(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.RequestURI())
		switch r.URL.Path {
		case "/job-history/gs/bkt/logs/my-job":
			io.WriteString(w, jobHistoryPage)
		case "/gcs/bkt/logs/my-job/1/artifacts/":
			if r.URL.Query().Get("marker") == "logs/my-job/1/artifacts/e2e/" {
				io.WriteString(w, listingPage("bkt", "logs/my-job/1/artifacts/",
					listingRowHTML("bkt", "logs/my-job/1/artifacts/z.txt", "file", "z.txt", "1", "Thu, 22 Oct 2026 12:43:18 UTC")))
				return
			}
			io.WriteString(w, listingPage("bkt", "logs/my-job/1/artifacts/",
				listingRowHTML("bkt", "logs/my-job/1/artifacts/e2e/", "dir", "e2e/", "-", "-"),
				nextButtonHTML("logs/my-job/1/artifacts/", "logs/my-job/1/artifacts/e2e/")))
		case "/gcs/bkt/logs/big/1/":
			io.WriteString(w, listingPage("bkt", "logs/big/1/",
				listingRowHTML("bkt", "logs/big/1/f", "file", "f", "1", "-"),
				nextButtonHTML("logs/big/1/", "m"+r.URL.Query().Get("marker"))))
		case "/gcs/bkt/logs/my-job/1/build-log.txt":
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, "line1\nline2\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := NewProw([]Deck{testDeck(srv)}, srv.Client())
	d := c.Decks()[0]
	ctx := context.Background()

	page, err := c.JobHistory(ctx, d, "logs", "my-job", "150")
	if err != nil {
		t.Fatalf("JobHistory: %v", err)
	}
	if len(page.Builds) != 2 {
		t.Errorf("got %d builds", len(page.Builds))
	}
	if gotPaths[0] != "/job-history/gs/bkt/logs/my-job?buildId=150" {
		t.Errorf("job history request = %q", gotPaths[0])
	}

	listing, err := c.List(ctx, d, "logs/my-job/1/artifacts")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listing.Entries) != 2 || listing.Entries[0].Name != "e2e" || listing.Entries[1].Name != "z.txt" || listing.Truncated {
		t.Errorf("unexpected listing: %+v", listing)
	}

	big, err := c.List(ctx, d, "logs/big/1/")
	if err != nil {
		t.Fatalf("List big: %v", err)
	}
	if !big.Truncated || len(big.Entries) != maxListPages {
		t.Errorf("expected %d entries and truncated, got %d %v", maxListPages, len(big.Entries), big.Truncated)
	}

	obj, err := c.Open(ctx, d, "logs/my-job/1/build-log.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	body, _ := io.ReadAll(obj.Body)
	obj.Body.Close()
	if string(body) != "line1\nline2\n" || obj.ContentType != "text/plain" {
		t.Errorf("unexpected object %q %q", body, obj.ContentType)
	}

	_, err = c.Open(ctx, d, "logs/my-job/1/missing.txt")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
		t.Errorf("expected a 404 HTTPError, got %v", err)
	}
}
