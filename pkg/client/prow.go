package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Deck is a Prow deck together with the gcsweb front end of the GCS bucket
// its jobs report to.
type Deck struct {
	Name      string   `json:"name"`
	DeckURL   string   `json:"deck_url"`
	GCSWebURL string   `json:"gcsweb_url"`
	Buckets   []string `json:"buckets"` // Bucket() (the first) is used unless a run URL names another
	// BucketAliases are old bucket names still found in deck links; runs
	// under them are read from Bucket().
	BucketAliases []string `json:"bucket_aliases,omitempty"`
	// ExtraHosts are other hosts that serve the same bucket, such as the
	// host gcsweb redirects to. They are accepted in run URLs.
	ExtraHosts []string `json:"extra_hosts,omitempty"`
}

// DefaultDecks are the decks known out of the box.
var DefaultDecks = []Deck{
	{
		Name:      "public",
		DeckURL:   "https://prow.ci.openshift.org",
		GCSWebURL: "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com",
		Buckets:   []string{"test-platform-results-public"},
		// Jobs moved from test-platform-results to test-platform-results-public;
		// deck still links to runs under the old name.
		BucketAliases: []string{"test-platform-results"},
		ExtraHosts:    []string{"gcs.ci.openshift.org", "storage.googleapis.com"},
	},
}

// Bucket returns the bucket used to read objects of this deck.
func (d Deck) Bucket() string {
	if len(d.Buckets) == 0 {
		return ""
	}
	return d.Buckets[0]
}

// forBucket returns the deck reading from bucket b, which must be one of
// its buckets or aliases.
func (d Deck) forBucket(b string) (Deck, bool) {
	for _, x := range d.Buckets {
		if x == b {
			d.Buckets = []string{b}
			return d, true
		}
	}
	for _, x := range d.BucketAliases {
		if x == b {
			return d, true
		}
	}
	return d, false
}

func (d Deck) hosts() []string {
	hosts := append([]string{}, d.ExtraHosts...)
	for _, raw := range []string{d.DeckURL, d.GCSWebURL} {
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			hosts = append(hosts, u.Host)
		}
	}
	return hosts
}

// Listing is a gcsweb directory listing.
type Listing struct {
	Entries []ArtifactEntry
	// Truncated is set when the directory has more pages than were read.
	Truncated bool
}

// ArtifactEntry is one entry of a gcsweb directory listing.
type ArtifactEntry struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // "dir" or "file"
	Size     int64  `json:"size,omitempty"`
	Modified string `json:"modified,omitempty"`
	Path     string `json:"path"` // relative to the bucket root
}

// JobHistoryBuild is one run from a deck job-history page.
type JobHistoryBuild struct {
	SpyglassLink string        `json:"SpyglassLink"`
	ID           string        `json:"ID"`
	Started      time.Time     `json:"Started"`
	Duration     time.Duration `json:"Duration"`
	Result       string        `json:"Result"`
	Refs         *JobRefs      `json:"Refs"`
}

// JobRefs are the git refs a run tested.
type JobRefs struct {
	Org     string    `json:"org"`
	Repo    string    `json:"repo"`
	BaseRef string    `json:"base_ref"`
	BaseSHA string    `json:"base_sha"`
	Pulls   []JobPull `json:"pulls"`
}

// JobPull is a pull request tested by a run.
type JobPull struct {
	Number int    `json:"number"`
	Author string `json:"author"`
	SHA    string `json:"sha"`
}

// JobHistoryPage is one page of a deck job-history page.
type JobHistoryPage struct {
	Builds []JobHistoryBuild
	// OlderBuildID, when set, is the buildId of the page with older runs.
	OlderBuildID string
}

// Object is an opened GCS object. Callers must close Body.
type Object struct {
	Body        io.ReadCloser
	ContentType string
	URL         string
}

// Prow reads job history from Prow decks and job artifacts from gcsweb.
type Prow interface {
	Decks() []Deck
	// JobHistory reads a job-history page. dir is "pr-logs/directory" for
	// presubmits or "logs" for periodics and postsubmits.
	JobHistory(ctx context.Context, deck Deck, dir, job, beforeBuildID string) (*JobHistoryPage, error)
	// List returns the entries of a directory of the deck's bucket.
	List(ctx context.Context, deck Deck, dirPath string) (*Listing, error)
	// Open opens an object of the deck's bucket for reading.
	Open(ctx context.Context, deck Deck, objectPath string) (*Object, error)
}

type prowClient struct {
	decks      []Deck
	httpClient *http.Client
}

func NewProw(decks []Deck, httpClient *http.Client) Prow {
	return &prowClient{decks: decks, httpClient: httpClient}
}

func (c *prowClient) Decks() []Deck { return c.decks }

func (c *prowClient) JobHistory(ctx context.Context, deck Deck, dir, job, beforeBuildID string) (*JobHistoryPage, error) {
	u, err := url.Parse(fmt.Sprintf("%s/job-history/gs/%s/%s/%s", deck.DeckURL, deck.Bucket(), dir, url.PathEscape(job)))
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if beforeBuildID != "" {
		u.RawQuery = url.Values{"buildId": {beforeBuildID}}.Encode()
	}
	body, err := c.get(ctx, deck, u.String())
	if err != nil {
		return nil, err
	}
	return ParseJobHistory(body)
}

// maxListPages bounds how many gcsweb pages one List call follows.
const maxListPages = 10

func (c *prowClient) List(ctx context.Context, deck Deck, dirPath string) (*Listing, error) {
	dirPath = strings.TrimSuffix(strings.TrimPrefix(dirPath, "/"), "/") + "/"
	base := fmt.Sprintf("%s/gcs/%s/%s", deck.GCSWebURL, deck.Bucket(), escapePath(dirPath))
	listing := &Listing{}
	marker := ""
	for page := 0; page < maxListPages; page++ {
		u := base
		if marker != "" {
			u += "?" + url.Values{"marker": {marker}}.Encode()
		}
		body, err := c.get(ctx, deck, u)
		if err != nil {
			return nil, err
		}
		entries, next, err := ParseGCSWebListing(body, deck.Bucket())
		if err != nil {
			return nil, err
		}
		listing.Entries = append(listing.Entries, entries...)
		if next == "" {
			return listing, nil
		}
		marker = next
	}
	listing.Truncated = true
	return listing, nil
}

func (c *prowClient) Open(ctx context.Context, deck Deck, objectPath string) (*Object, error) {
	rawURL := fmt.Sprintf("%s/gcs/%s/%s", deck.GCSWebURL, deck.Bucket(), escapePath(strings.TrimPrefix(objectPath, "/")))
	resp, err := c.do(ctx, deck, rawURL)
	if err != nil {
		return nil, err
	}
	return &Object{Body: resp.Body, ContentType: resp.Header.Get("Content-Type"), URL: rawURL}, nil
}

// maxPageBytes bounds deck pages and gcsweb listings, which are small.
const maxPageBytes = 16 << 20

func (c *prowClient) get(ctx context.Context, deck Deck, rawURL string) ([]byte, error) {
	resp, err := c.do(ctx, deck, rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	return body, nil
}

func (c *prowClient) do(ctx context.Context, deck Deck, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	// Follow redirects only to hosts of the same deck, and never from
	// https to http. The shared client is left as it is for other services.
	hc := *c.httpClient
	hosts := deck.hosts()
	hc.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if !containsHost(hosts, r.URL.Host) {
			return fmt.Errorf("redirect to %s is outside deck %q", r.URL.Host, deck.Name)
		}
		if via[0].URL.Scheme == "https" && r.URL.Scheme != "https" {
			return fmt.Errorf("redirect from https to %s refused", r.URL.Scheme)
		}
		return nil
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, &HTTPError{StatusCode: resp.StatusCode, Body: body}
	}
	return resp, nil
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

var (
	allBuildsPattern = regexp.MustCompile(`(?m)^\s*var allBuilds = (.*?);?\s*$`)
	buildIDLink      = regexp.MustCompile(`[?&]buildId=(\d+)`)
)

// ParseJobHistory extracts the runs embedded in a deck job-history page.
func ParseJobHistory(page []byte) (*JobHistoryPage, error) {
	m := allBuildsPattern.FindSubmatch(page)
	if m == nil {
		return nil, fmt.Errorf("job history page has no build list")
	}
	var builds []JobHistoryBuild
	if err := json.Unmarshal(m[1], &builds); err != nil {
		return nil, fmt.Errorf("parsing job history: %w", err)
	}
	result := &JobHistoryPage{Builds: builds}
	if len(builds) == 0 {
		return result, nil
	}
	oldest, err := strconv.ParseUint(builds[len(builds)-1].ID, 10, 64)
	if err != nil {
		return result, nil
	}
	// The page links to newer and older pages; the older one has the
	// highest buildId below the oldest run shown.
	var older uint64
	for _, l := range buildIDLink.FindAllSubmatch(page, -1) {
		id, err := strconv.ParseUint(string(l[1]), 10, 64)
		if err == nil && id < oldest && id > older {
			older = id
		}
	}
	if older > 0 {
		result.OlderBuildID = strconv.FormatUint(older, 10)
	}
	return result, nil
}

var nextPageLink = regexp.MustCompile(`<a href="[^"]*\?marker=([^"]*)"\s*class="pure-button next-button"`)

var listingRow = regexp.MustCompile(`<a href="/gcs/([^"]+)"><img src="/icons/(dir|file)\.png">\s*([^<]*)</a></div>\s*<div[^>]*>([^<]*)</div>\s*<div[^>]*>([^<]*)</div>`)

// ParseGCSWebListing parses a gcsweb directory page. next is the marker of
// the next page, or empty on the last page.
func ParseGCSWebListing(page []byte, bucket string) (entries []ArtifactEntry, next string, err error) {
	if !strings.Contains(string(page), "resource-grid") {
		return nil, "", fmt.Errorf("not a gcsweb directory listing")
	}
	if m := nextPageLink.FindSubmatch(page); m != nil {
		if next, err = url.QueryUnescape(html.UnescapeString(string(m[1]))); err != nil {
			return nil, "", fmt.Errorf("parsing next page marker: %w", err)
		}
	}
	for _, m := range listingRow.FindAllSubmatch(page, -1) {
		full, err := url.PathUnescape(html.UnescapeString(string(m[1])))
		if err != nil {
			continue
		}
		e := ArtifactEntry{
			Name: strings.TrimSuffix(strings.TrimSpace(html.UnescapeString(string(m[3]))), "/"),
			Type: string(m[2]),
			Path: strings.TrimPrefix(full, bucket+"/"),
		}
		if size, err := strconv.ParseInt(strings.TrimSpace(string(m[4])), 10, 64); err == nil {
			e.Size = size
		}
		if mod := strings.TrimSpace(string(m[5])); mod != "-" {
			e.Modified = mod
		}
		entries = append(entries, e)
	}
	return entries, next, nil
}

// RunRef identifies one run of a Prow job in a deck's bucket.
type RunRef struct {
	Deck    Deck
	Path    string // run root relative to the bucket, without trailing slash
	Job     string
	BuildID string
	OrgRepo string // org_repo for presubmits, empty otherwise
	PR      string // PR number for presubmits, empty otherwise
}

// SpyglassURL is the deck page of the run.
func (r RunRef) SpyglassURL() string {
	return fmt.Sprintf("%s/view/gs/%s/%s", r.Deck.DeckURL, r.Deck.Bucket(), r.Path)
}

// ObjectURL is the gcsweb URL of a path relative to the run root.
func (r RunRef) ObjectURL(rel string) string {
	return fmt.Sprintf("%s/gcs/%s/%s/%s", r.Deck.GCSWebURL, r.Deck.Bucket(), r.Path, strings.TrimPrefix(rel, "/"))
}

var (
	presubmitPath = regexp.MustCompile(`^pr-logs/pull/([^/]+)/(\d+)/([^/]+)/(\d+)(?:/|$)`)
	logsPath      = regexp.MustCompile(`^logs/([^/]+)/(\d+)(?:/|$)`)
	rehearseJob   = regexp.MustCompile(`^rehearse-(\d+)-`)
	digits        = regexp.MustCompile(`^\d+$`)
	namePattern   = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// ParseRunURL resolves a deck (Spyglass), gcsweb or GCS URL of a run, or of
// any file inside it, to a RunRef. Only hosts of the given decks are accepted.
func ParseRunURL(decks []Deck, rawURL string) (RunRef, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return RunRef{}, fmt.Errorf("invalid run URL %q", rawURL)
	}
	p := strings.TrimPrefix(u.EscapedPath(), "/")
	for _, prefix := range []string{"view/gs/", "gcs/"} {
		if strings.HasPrefix(p, prefix) {
			p = strings.TrimPrefix(p, prefix)
			break
		}
	}
	if unescaped, err := url.PathUnescape(p); err == nil {
		p = unescaped
	}
	bucket, rest, _ := strings.Cut(p, "/")
	for _, d := range decks {
		if !containsHost(d.hosts(), u.Host) {
			continue
		}
		d, ok := d.forBucket(bucket)
		if !ok {
			continue
		}
		ref, err := runRefFromPath(d, rest)
		if err != nil {
			return RunRef{}, fmt.Errorf("%w in %q", err, rawURL)
		}
		return ref, nil
	}
	return RunRef{}, fmt.Errorf("run URL %q is not on a known deck (%s)", rawURL, deckNames(decks))
}

func runRefFromPath(d Deck, p string) (RunRef, error) {
	if m := presubmitPath.FindStringSubmatch(p); m != nil && validName(m[1]) && validName(m[3]) {
		return RunRef{Deck: d, Path: strings.TrimSuffix(m[0], "/"), OrgRepo: m[1], PR: m[2], Job: m[3], BuildID: m[4]}, nil
	}
	if m := logsPath.FindStringSubmatch(p); m != nil && validName(m[1]) {
		return RunRef{Deck: d, Path: strings.TrimSuffix(m[0], "/"), Job: m[1], BuildID: m[2]}, nil
	}
	return RunRef{}, fmt.Errorf("no job run path (pr-logs/pull/<org_repo>/<pr>/<job>/<build_id> or logs/<job>/<build_id>)")
}

// NewRunRef builds a RunRef from a job name and build ID. Presubmit runs
// need the PR number and org_repo ("org_repo" or "org/repo"); for
// rehearsal jobs ("rehearse-<pr>-...") both are derived from the job name.
func NewRunRef(d Deck, job, buildID, pr, orgRepo string) (RunRef, error) {
	if !digits.MatchString(buildID) {
		return RunRef{}, fmt.Errorf("build_id must be numeric")
	}
	if !validName(job) {
		return RunRef{}, fmt.Errorf("invalid job name %q", job)
	}
	if m := rehearseJob.FindStringSubmatch(job); m != nil {
		if pr == "" {
			pr = m[1]
		}
		if orgRepo == "" {
			orgRepo = "openshift_release"
		}
	}
	if pr == "" {
		if strings.HasPrefix(job, "pull-ci-") {
			return RunRef{}, fmt.Errorf("presubmit job %q needs pr and org_repo", job)
		}
		return RunRef{Deck: d, Path: fmt.Sprintf("logs/%s/%s", job, buildID), Job: job, BuildID: buildID}, nil
	}
	if !digits.MatchString(pr) {
		return RunRef{}, fmt.Errorf("pr must be numeric")
	}
	orgRepo = strings.Replace(orgRepo, "/", "_", 1)
	if !validName(orgRepo) {
		return RunRef{}, fmt.Errorf("presubmit job %q needs org_repo (for example openshift_release)", job)
	}
	return RunRef{
		Deck: d, Path: fmt.Sprintf("pr-logs/pull/%s/%s/%s/%s", orgRepo, pr, job, buildID),
		Job: job, BuildID: buildID, OrgRepo: orgRepo, PR: pr,
	}, nil
}

// validName accepts job and org_repo names: one path segment, not . or ..
func validName(s string) bool {
	return namePattern.MatchString(s) && s != "." && s != ".."
}

// DeckByName returns the deck with the given name.
func DeckByName(decks []Deck, name string) (Deck, error) {
	for _, d := range decks {
		if d.Name == name {
			return d, nil
		}
	}
	return Deck{}, fmt.Errorf("unknown deck %q (known: %s)", name, deckNames(decks))
}

func deckNames(decks []Deck) string {
	names := make([]string, 0, len(decks))
	for _, d := range decks {
		names = append(names, d.Name)
	}
	return strings.Join(names, ", ")
}

func containsHost(hosts []string, host string) bool {
	for _, h := range hosts {
		if strings.EqualFold(h, host) {
			return true
		}
	}
	return false
}
