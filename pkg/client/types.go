package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
)

// FilterFields keeps only the named fields in the JSON output. For arrays,
// each element is filtered. For objects with a "rows" key, the rows are
// filtered while preserving the wrapper. Returns data unchanged on empty
// fields string or parse error.
func FilterFields(data []byte, fields string) ([]byte, error) {
	if fields == "" {
		return data, nil
	}
	keep := make(map[string]bool)
	for f := range strings.SplitSeq(fields, ",") {
		if s := strings.TrimSpace(f); s != "" {
			keep[s] = true
		}
	}
	if len(keep) == 0 {
		return data, nil
	}

	var arr []map[string]any
	if unmarshalUseNumber(data, &arr) == nil {
		for _, m := range arr {
			stripKeys(m, keep)
		}
		return json.Marshal(arr)
	}

	var obj map[string]any
	if unmarshalUseNumber(data, &obj) == nil {
		if rows, ok := obj["rows"].([]any); ok {
			for _, row := range rows {
				if m, ok := row.(map[string]any); ok {
					stripKeys(m, keep)
				}
			}
			return json.Marshal(obj)
		}
		stripKeys(obj, keep)
		return json.Marshal(obj)
	}

	return data, nil
}

// unmarshalUseNumber is json.Unmarshal that keeps numbers as json.Number,
// so large integers such as Prow job run IDs don't lose precision.
func unmarshalUseNumber(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("unexpected data after top-level JSON value")
	}
	return nil
}

func stripKeys(m map[string]any, keep map[string]bool) {
	for k := range m {
		if !keep[k] {
			delete(m, k)
		}
	}
}

// PaginateArray slices a JSON array and wraps the result with pagination
// metadata. Use for endpoints where the upstream API ignores perPage/page.
func PaginateArray(data []byte, limit, page int) ([]byte, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	if limit < 1 {
		limit = 1
	}
	if page < 1 {
		page = 1
	}
	total := len(items)
	start := min((page-1)*limit, total)
	end := min(start+limit, total)
	result := struct {
		Rows      []json.RawMessage `json:"rows"`
		Page      int               `json:"page"`
		PageSize  int               `json:"page_size"`
		TotalRows int               `json:"total_rows"`
	}{
		Rows:      items[start:end],
		Page:      page,
		PageSize:  limit,
		TotalRows: total,
	}
	return json.Marshal(result)
}

// ReshapeJSON unmarshals raw JSON into T then re-marshals, dropping
// any fields not declared in T.
func ReshapeJSON[T any](data []byte) ([]byte, error) {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// JobRunRow is the trimmed representation of a Sippy job run.
// Source has ~25 fields; this keeps ~13 essential ones.
type JobRunRow struct {
	ProwID                int64    `json:"prow_id"`
	Name                  string   `json:"job"`
	BriefName             string   `json:"brief_name"`
	Variants              []string `json:"variants,omitempty"`
	URL                   string   `json:"url"`
	TestFailures          int      `json:"test_failures"`
	FailedTestNames       []string `json:"failed_test_names,omitempty"`
	TestFlakes            int      `json:"test_flakes"`
	Failed                bool     `json:"failed"`
	InfrastructureFailure bool     `json:"infrastructure_failure"`
	KnownFailure          bool     `json:"known_failure"`
	Succeeded             bool     `json:"succeeded"`
	Timestamp             int64    `json:"timestamp"`
	OverallResult         string   `json:"overall_result"`
	Labels                []string `json:"labels,omitempty"`
}

// JobRunsResponse wraps paginated job run results.
type JobRunsResponse struct {
	Rows      []JobRunRow `json:"rows"`
	PageSize  int         `json:"page_size"`
	Page      int         `json:"page"`
	TotalRows int         `json:"total_rows"`
}

// JobReportRow is the trimmed representation of a Sippy job report entry.
// Source has ~16 fields; this keeps ~10.
type JobReportRow struct {
	Name                              string   `json:"name"`
	BriefName                         string   `json:"brief_name,omitempty"`
	Variants                          []string `json:"variants,omitempty"`
	LastPass                          string   `json:"last_pass,omitempty"`
	CurrentPassPercentage             float64  `json:"current_pass_percentage"`
	CurrentProjectedPassPercentage    float64  `json:"current_projected_pass_percentage"`
	CurrentRuns                       int      `json:"current_runs"`
	CurrentFails                      int      `json:"current_fails"`
	PreviousPassPercentage            float64  `json:"previous_pass_percentage"`
	PreviousProjectedPassPercentage   float64  `json:"previous_projected_pass_percentage"`
	PreviousRuns                      int      `json:"previous_runs"`
	NetImprovement                    float64  `json:"net_improvement"`
	AverageRetestsToMerge             float64  `json:"average_retests_to_merge"`
	OpenBugs                          int      `json:"open_bugs"`
}

// TestReportMetrics holds the derivable current/previous/net fields
// that are nested under a "metrics" key in the trimmed output.
// The source JSON has these as flat top-level fields.
type TestReportMetrics struct {
	CurrentFailurePercentage  float64 `json:"current_failure_percentage"`
	CurrentFlakePercentage    float64 `json:"current_flake_percentage"`
	CurrentWorkingPercentage  float64 `json:"current_working_percentage"`
	PreviousSuccesses         int     `json:"previous_successes"`
	PreviousFailures          int     `json:"previous_failures"`
	PreviousFlakes            int     `json:"previous_flakes"`
	PreviousPassPercentage    float64 `json:"previous_pass_percentage"`
	PreviousFailurePercentage float64 `json:"previous_failure_percentage"`
	PreviousFlakePercentage   float64 `json:"previous_flake_percentage"`
	PreviousWorkingPercentage float64 `json:"previous_working_percentage"`
	PreviousRuns              int     `json:"previous_runs"`
	NetFailureImprovement     float64 `json:"net_failure_improvement"`
	NetFlakeImprovement       float64 `json:"net_flake_improvement"`
	NetWorkingImprovement     float64 `json:"net_working_improvement"`
}

// TestReportRow reshapes a Sippy test report entry. The source has ~28 flat
// fields; the output nests the derivable current/previous/net fields under
// a "metrics" sub-object to separate primary data from secondary detail.
type TestReportRow struct {
	Name                  string            `json:"name"`
	SuiteName             string            `json:"suite_name,omitempty"`
	JiraComponent         string            `json:"jira_component,omitempty"`
	Variants              []string          `json:"variants,omitempty"`
	CurrentPassPercentage float64           `json:"current_pass_percentage"`
	CurrentRuns           int               `json:"current_runs"`
	CurrentSuccesses      int               `json:"current_successes"`
	CurrentFailures       int               `json:"current_failures"`
	CurrentFlakes         int               `json:"current_flakes"`
	NetImprovement        float64           `json:"net_improvement"`
	OpenBugs              int                `json:"open_bugs"`
	Metrics               *TestReportMetrics `json:"metrics,omitempty"`
}

func (t *TestReportRow) UnmarshalJSON(data []byte) error {
	type flat struct {
		Name                      string   `json:"name"`
		SuiteName                 string   `json:"suite_name"`
		JiraComponent             string   `json:"jira_component"`
		Variants                  []string `json:"variants"`
		CurrentPassPercentage     float64  `json:"current_pass_percentage"`
		CurrentRuns               int      `json:"current_runs"`
		CurrentSuccesses          int      `json:"current_successes"`
		CurrentFailures           int      `json:"current_failures"`
		CurrentFlakes             int      `json:"current_flakes"`
		NetImprovement            float64  `json:"net_improvement"`
		OpenBugs                  int      `json:"open_bugs"`
		CurrentFailurePercentage  float64  `json:"current_failure_percentage"`
		CurrentFlakePercentage    float64  `json:"current_flake_percentage"`
		CurrentWorkingPercentage  float64  `json:"current_working_percentage"`
		PreviousSuccesses         int      `json:"previous_successes"`
		PreviousFailures          int      `json:"previous_failures"`
		PreviousFlakes            int      `json:"previous_flakes"`
		PreviousPassPercentage    float64  `json:"previous_pass_percentage"`
		PreviousFailurePercentage float64  `json:"previous_failure_percentage"`
		PreviousFlakePercentage   float64  `json:"previous_flake_percentage"`
		PreviousWorkingPercentage float64  `json:"previous_working_percentage"`
		PreviousRuns              int      `json:"previous_runs"`
		NetFailureImprovement     float64  `json:"net_failure_improvement"`
		NetFlakeImprovement       float64  `json:"net_flake_improvement"`
		NetWorkingImprovement     float64  `json:"net_working_improvement"`
	}
	var f flat
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	t.Name = f.Name
	t.SuiteName = f.SuiteName
	t.JiraComponent = f.JiraComponent
	t.Variants = f.Variants
	t.CurrentPassPercentage = f.CurrentPassPercentage
	t.CurrentRuns = f.CurrentRuns
	t.CurrentSuccesses = f.CurrentSuccesses
	t.CurrentFailures = f.CurrentFailures
	t.CurrentFlakes = f.CurrentFlakes
	t.NetImprovement = f.NetImprovement
	t.OpenBugs = f.OpenBugs
	t.Metrics = &TestReportMetrics{
		CurrentFailurePercentage:  f.CurrentFailurePercentage,
		CurrentFlakePercentage:    f.CurrentFlakePercentage,
		CurrentWorkingPercentage:  f.CurrentWorkingPercentage,
		PreviousSuccesses:         f.PreviousSuccesses,
		PreviousFailures:          f.PreviousFailures,
		PreviousFlakes:            f.PreviousFlakes,
		PreviousPassPercentage:    f.PreviousPassPercentage,
		PreviousFailurePercentage: f.PreviousFailurePercentage,
		PreviousFlakePercentage:   f.PreviousFlakePercentage,
		PreviousWorkingPercentage: f.PreviousWorkingPercentage,
		PreviousRuns:              f.PreviousRuns,
		NetFailureImprovement:     f.NetFailureImprovement,
		NetFlakeImprovement:       f.NetFlakeImprovement,
		NetWorkingImprovement:     f.NetWorkingImprovement,
	}
	return nil
}

// ReshapeTestReport reshapes test report data with optional metrics inclusion.
func ReshapeTestReport(data []byte, includeMetrics bool) ([]byte, error) {
	var rows []TestReportRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, err
	}
	if !includeMetrics {
		for i := range rows {
			rows[i].Metrics = nil
		}
	}
	return json.Marshal(rows)
}

// ---------------------------------------------------------------------------
// Job Run Summary — /api/job/run/summary
// ---------------------------------------------------------------------------

type JobRunSummary struct {
	ID                    uint64                  `json:"id"`
	Name                  string                  `json:"name"`
	Release               string                  `json:"release"`
	Cluster               string                  `json:"cluster"`
	URL                   string                  `json:"url"`
	GCSBucket             string                  `json:"gcsBucket"`
	StartTime             string                  `json:"startTime"`
	Duration              int64                   `json:"duration"`
	DurationSeconds       float64                 `json:"durationSeconds"`
	OverallResult         string                  `json:"overallResult"`
	Reason                string                  `json:"reason"`
	Succeeded             bool                    `json:"succeeded"`
	Failed                bool                    `json:"failed"`
	InfrastructureFailure bool                    `json:"infrastructureFailure"`
	KnownFailure          bool                    `json:"knownFailure"`
	TestCount             int                     `json:"testCount"`
	TestFailureCount      int                     `json:"testFailureCount"`
	TestFailures          map[string]string       `json:"testFailures,omitempty"`
	Variants              []string                `json:"variants,omitempty"`
	ClusterOperators      []ClusterOperatorStatus `json:"clusterOperators,omitempty"`
}

type ClusterOperatorStatus struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// ---------------------------------------------------------------------------
// Test Details — /api/tests/details
// ---------------------------------------------------------------------------

type TestDetailsResponse struct {
	Title       string                              `json:"title"`
	Description string                              `json:"description"`
	ColumnNames []string                            `json:"column_names"`
	Tests       map[string]map[string]TestDetailCell `json:"tests"`
}

type TestDetailCell struct {
	Name                   string  `json:"name"`
	CurrentPassPercentage  float64 `json:"current_pass_percentage"`
	CurrentSuccesses       int     `json:"current_successes"`
	CurrentFailures        int     `json:"current_failures"`
	CurrentFlakes          int     `json:"current_flakes"`
	CurrentRuns            int     `json:"current_runs"`
	PreviousPassPercentage float64 `json:"previous_pass_percentage"`
	PreviousSuccesses      int     `json:"previous_successes"`
	PreviousFailures       int     `json:"previous_failures"`
	PreviousFlakes         int     `json:"previous_flakes"`
	PreviousRuns           int     `json:"previous_runs"`
}

// ---------------------------------------------------------------------------
// Pull Request Impact — /api/pull_requests/test_results
// ---------------------------------------------------------------------------

type PRTestResult struct {
	ProwJobBuildID string `json:"prowjob_build_id"`
	ProwJobName    string `json:"prowjob_name"`
	ProwJobURL     string `json:"prowjob_url"`
	PRSha          string `json:"pr_sha"`
	ProwJobStart   string `json:"prowjob_start"`
	TestName       string `json:"test_name"`
	TestSuite      string `json:"test_suite"`
	Success        bool   `json:"success"`
	Flaked         bool   `json:"flaked"`
	FailureContent string `json:"failure_content"`
}

// ---------------------------------------------------------------------------
// Pull Requests — /api/pull_requests
// ---------------------------------------------------------------------------

type PullRequestRow struct {
	ID                         int     `json:"id"`
	Org                        string  `json:"org"`
	Repo                       string  `json:"repo"`
	Number                     int     `json:"number"`
	Title                      string  `json:"title"`
	Author                     string  `json:"author"`
	SHA                        string  `json:"sha"`
	Link                       string  `json:"link"`
	MergedAt                   *string `json:"merged_at"`
	FirstCIPayload             string  `json:"first_ci_payload"`
	FirstCIPayloadPhase        string  `json:"first_ci_payload_phase"`
	FirstCIPayloadRelease      string  `json:"first_ci_payload_release"`
	FirstNightlyPayload        string  `json:"first_nightly_payload"`
	FirstNightlyPayloadPhase   string  `json:"first_nightly_payload_phase"`
	FirstNightlyPayloadRelease string  `json:"first_nightly_payload_release"`
}

// ---------------------------------------------------------------------------
// Payload Diff — /api/payloads/diff
// ---------------------------------------------------------------------------

type PayloadDiffRow struct {
	URL           string `json:"url"`
	PullRequestID string `json:"pull_request_id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	BugURL        string `json:"bug_url"`
}

// ---------------------------------------------------------------------------
// Payload Test Failures — /api/payloads/test_failures
// ---------------------------------------------------------------------------

type PayloadTestFailure struct {
	Name                string                       `json:"name"`
	ID                  int                          `json:"id"`
	FailureCount        int                          `json:"failure_count"`
	BlockerScore        int                          `json:"blocker_score"`
	BlockerScoreReasons []string                     `json:"blocker_score_reasons"`
	FailedPayloads      map[string]PayloadFailedJobs `json:"failed_payloads"`
}

type PayloadFailedJobs struct {
	FailedJobs    []string `json:"failed_jobs"`
	FailedJobRuns []string `json:"failed_job_runs"`
}

// ---------------------------------------------------------------------------
// Releases — /api/releases
// ---------------------------------------------------------------------------

type ReleasesResponse struct {
	Releases     []string                    `json:"releases"`
	GADates      map[string]string           `json:"ga_dates,omitempty"`
	Dates        map[string]ReleaseDates     `json:"dates"`
	LastUpdated  string                      `json:"last_updated"`
	ReleaseAttrs map[string]ReleaseAttrsEntry `json:"release_attrs"`
}

type ReleaseDates struct {
	GA               string `json:"ga,omitempty"`
	DevelopmentStart string `json:"development_start,omitempty"`
}

type ReleaseAttrsEntry struct {
	Name             string          `json:"name"`
	GA               string          `json:"ga,omitempty"`
	DevelopmentStart string          `json:"development_start,omitempty"`
	PreviousRelease  string          `json:"previous_release"`
	Capabilities     map[string]bool `json:"capabilities"`
	Product          string          `json:"product"`
}

// ---------------------------------------------------------------------------
// Release Health — /api/releases/health
// ---------------------------------------------------------------------------

type ReleaseHealthRow struct {
	ReleaseTag           string          `json:"release_tag"`
	Release              string          `json:"release"`
	Stream               string          `json:"stream"`
	Architecture         string          `json:"architecture"`
	Phase                string          `json:"phase"`
	Forced               bool            `json:"forced"`
	ReleaseTime          string          `json:"release_time"`
	PreviousReleaseTag   string          `json:"previous_release_tag"`
	KubernetesVersion    string          `json:"kubernetes_version"`
	CurrentOSVersion     string          `json:"current_os_version"`
	PreviousOSVersion    string          `json:"previous_os_version"`
	CurrentOSURL         string          `json:"current_os_url"`
	PreviousOSURL        string          `json:"previous_os_url"`
	OSDiffURL            string          `json:"os_diff_url"`
	RejectReason         string          `json:"reject_reason"`
	RejectReasonNote     string          `json:"reject_reason_note"`
	RejectReasons        []string        `json:"reject_reasons"`
	LastPhase            string          `json:"last_phase"`
	Count                int             `json:"count"`
	PhaseCounts          PhaseCounts     `json:"phase_counts"`
	AcceptanceStatistics AcceptanceStats `json:"acceptance_statistics"`
}

type PhaseCounts struct {
	CurrentWeek PhaseCount `json:"current_week"`
	Total       PhaseCount `json:"total"`
}

type PhaseCount struct {
	Accepted int `json:"accepted"`
	Rejected int `json:"rejected"`
}

type AcceptanceStats struct {
	CurrentWeek AcceptanceTiming `json:"current_week"`
	Total       AcceptanceTiming `json:"total"`
}

type AcceptanceTiming struct {
	MinSecondsBetween  int64 `json:"min_seconds_between"`
	MeanSecondsBetween int64 `json:"mean_seconds_between"`
	MaxSecondsBetween  int64 `json:"max_seconds_between"`
}

// ---------------------------------------------------------------------------
// Health — /api/health
// ---------------------------------------------------------------------------

type HealthResponse struct {
	Indicators         map[string]HealthIndicator `json:"indicators"`
	Variants           VariantHealth              `json:"variants"`
	LastUpdated        string                     `json:"last_updated"`
	Promotions         map[string]*string         `json:"promotions"`
	Warnings           []string                   `json:"warnings"`
	CurrentStatistics  HealthStatistics           `json:"current_statistics"`
	PreviousStatistics HealthStatistics           `json:"previous_statistics"`
}

type HealthIndicator struct {
	Name                      string   `json:"name"`
	SuiteName                 string   `json:"suite_name"`
	Variants                  []string `json:"variants,omitempty"`
	JiraComponent             string   `json:"jira_component"`
	CurrentSuccesses          int      `json:"current_successes"`
	CurrentFailures           int      `json:"current_failures"`
	CurrentFlakes             int      `json:"current_flakes"`
	CurrentPassPercentage     float64  `json:"current_pass_percentage"`
	CurrentFailurePercentage  float64  `json:"current_failure_percentage"`
	CurrentFlakePercentage    float64  `json:"current_flake_percentage"`
	CurrentWorkingPercentage  float64  `json:"current_working_percentage"`
	CurrentRuns               int      `json:"current_runs"`
	PreviousSuccesses         int      `json:"previous_successes"`
	PreviousFailures          int      `json:"previous_failures"`
	PreviousFlakes            int      `json:"previous_flakes"`
	PreviousPassPercentage    float64  `json:"previous_pass_percentage"`
	PreviousFailurePercentage float64  `json:"previous_failure_percentage"`
	PreviousFlakePercentage   float64  `json:"previous_flake_percentage"`
	PreviousWorkingPercentage float64  `json:"previous_working_percentage"`
	PreviousRuns              int      `json:"previous_runs"`
	NetFailureImprovement     float64  `json:"net_failure_improvement"`
	NetFlakeImprovement       float64  `json:"net_flake_improvement"`
	NetWorkingImprovement     float64  `json:"net_working_improvement"`
	NetImprovement            float64  `json:"net_improvement"`
	OpenBugs                  int      `json:"open_bugs"`
}

type VariantHealth struct {
	Current  VariantCounts `json:"current"`
	Previous VariantCounts `json:"previous"`
}

type VariantCounts struct {
	Success  int `json:"success"`
	Unstable int `json:"unstable"`
	Failed   int `json:"failed"`
}

type HealthStatistics struct {
	Mean              float64   `json:"mean"`
	StandardDeviation float64   `json:"standard_deviation"`
	Histogram         []int     `json:"histogram"`
	Quartiles         []float64 `json:"quartiles"`
	P95               float64   `json:"p95"`
}

// ---------------------------------------------------------------------------
// Component Readiness — /api/component_readiness
// ---------------------------------------------------------------------------

type ComponentReadinessResponse struct {
	GeneratedAt string         `json:"generated_at"`
	Rows        []ComponentRow `json:"rows"`
}

type ComponentRow struct {
	Component string            `json:"component"`
	Columns   []ComponentColumn `json:"columns"`
}

type ComponentColumn struct {
	Variants       map[string]string `json:"variants"`
	Status         int               `json:"status"`
	RegressedTests []RegressedTest   `json:"regressed_tests,omitempty"`
}

type RegressedTest struct {
	Component    string            `json:"component"`
	Capability   string            `json:"capability"`
	TestName     string            `json:"test_name"`
	TestSuite    string            `json:"test_suite"`
	TestID       string            `json:"test_id"`
	Variants     map[string]string `json:"variants"`
	Status       int               `json:"status"`
	Comparison   string            `json:"comparison"`
	Explanations []string          `json:"explanations"`
	SampleStats  TestStats         `json:"sample_stats"`
	BaseStats    TestStats         `json:"base_stats"`
	Links        map[string]string `json:"links"`
	FisherExact  float64           `json:"fisher_exact"`
	LastFailure  string            `json:"last_failure"`
	Regression   *Regression       `json:"regression,omitempty"`
}

type TestStats struct {
	Release      string  `json:"release"`
	Start        string  `json:"Start"`
	End          string  `json:"End"`
	SuccessCount int     `json:"success_count"`
	FailureCount int     `json:"failure_count"`
	FlakeCount   int     `json:"flake_count"`
	SuccessRate  float64 `json:"success_rate"`
}

// ---------------------------------------------------------------------------
// Regressions — /api/component_readiness/regressions[/{id}]
// ---------------------------------------------------------------------------

type NullTime struct {
	Time  string `json:"Time"`
	Valid bool   `json:"Valid"`
}

type Regression struct {
	ID           int                `json:"id"`
	Release      string             `json:"release"`
	BaseRelease  string             `json:"base_release"`
	Component    string             `json:"component"`
	Capability   string             `json:"capability"`
	CrossCompare bool               `json:"cross_compare"`
	TestID       string             `json:"test_id"`
	TestName     string             `json:"test_name"`
	Variants     []string           `json:"variants"`
	Opened       string             `json:"opened"`
	Closed       NullTime           `json:"closed"`
	Triages      []Triage           `json:"triages"`
	LastFailure  NullTime           `json:"last_failure"`
	MaxFailures  int                `json:"max_failures"`
	JobRuns      []RegressionJobRun `json:"job_runs,omitempty"`
	Views        []RegressionView   `json:"views,omitempty"`
	Links        map[string]string  `json:"links,omitempty"`
}

type Triage struct {
	ID               int      `json:"id"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	URL              string   `json:"url"`
	Description      string   `json:"description"`
	Type             string   `json:"type"`
	BugID            int      `json:"bug_id"`
	Resolved         NullTime `json:"resolved"`
	ResolutionReason string   `json:"resolution_reason"`
}

type RegressionView struct {
	TestRegressionID int      `json:"test_regression_id"`
	ViewName         string   `json:"view_name"`
	Active           bool     `json:"active"`
	OpenedAt         string   `json:"opened_at"`
	ClosedAt         NullTime `json:"closed_at"`
}

type RegressionJobRun struct {
	ID           int      `json:"id"`
	RegressionID int      `json:"regression_id"`
	ProwJobRunID string   `json:"prowjob_run_id"`
	ProwJobName  string   `json:"prowjob_name"`
	ProwJobURL   string   `json:"prowjob_url"`
	StartTime    string   `json:"start_time"`
	TestFailed   bool     `json:"test_failed"`
	TestFailures int      `json:"test_failures"`
	JobLabels    []string `json:"job_labels,omitempty"`
	JobSymptoms  []string `json:"job_symptoms,omitempty"`
}

// ---------------------------------------------------------------------------
// Regression Matches — /api/component_readiness/regressions/{id}/matches
// ---------------------------------------------------------------------------

type RegressionMatch struct {
	SimilarlyNamedTests []SimilarTest     `json:"similarly_named_tests"`
	ConfidenceLevel     int               `json:"confidence_level"`
	Links               map[string]string `json:"links"`
	Triage              *TriageExpanded   `json:"triage,omitempty"`
}

type SimilarTest struct {
	Regression   Regression `json:"regression"`
	EditDistance  int        `json:"edit_distance"`
}

type TriageExpanded struct {
	ID               int               `json:"id"`
	CreatedAt        string            `json:"created_at"`
	UpdatedAt        string            `json:"updated_at"`
	URL              string            `json:"url"`
	Description      string            `json:"description"`
	Type             string            `json:"type"`
	Bug              *JiraBug          `json:"bug,omitempty"`
	BugID            int               `json:"bug_id"`
	Links            map[string]string `json:"links,omitempty"`
	Regressions      []Regression      `json:"regressions,omitempty"`
	Resolved         NullTime          `json:"resolved"`
	ResolutionReason string            `json:"resolution_reason"`
}

type JiraBug struct {
	ID              int      `json:"id"`
	Key             string   `json:"key"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
	Status          string   `json:"status"`
	LastChangeTime  string   `json:"last_change_time"`
	Summary         string   `json:"summary"`
	AffectsVersions []string `json:"affects_versions,omitempty"`
	FixVersions     []string `json:"fix_versions,omitempty"`
	TargetVersions  []string `json:"target_versions,omitempty"`
	Components      []string `json:"components"`
	Labels          []string `json:"labels"`
	URL             string   `json:"url"`
	ReleaseBlocker  string   `json:"release_blocker"`
}

// ---------------------------------------------------------------------------
// Component Readiness Views — /api/component_readiness/views
// ---------------------------------------------------------------------------

type ComponentReadinessView struct {
	Name          string `json:"name"`
}

// ---------------------------------------------------------------------------
// Release Controller — /api/v1/releasestream/{stream}/tags
// ---------------------------------------------------------------------------

type ReleaseStreamTags struct {
	Name string       `json:"name"`
	Tags []ReleaseTag `json:"tags"`
}

type ReleaseTag struct {
	Name        string `json:"name"`
	Phase       string `json:"phase"`
	PullSpec    string `json:"pullSpec"`
	DownloadURL string `json:"downloadURL"`
}

// ---------------------------------------------------------------------------
// SearchCI — /v2/search
// ---------------------------------------------------------------------------

type SearchResponse struct {
	Results map[string]SearchTermResult `json:"results"`
}

type SearchTermResult struct {
	Matches []SearchMatch `json:"matches"`
}

type SearchMatch struct {
	Name         string   `json:"name"`
	Filename     string   `json:"filename"`
	URL          string   `json:"url"`
	Context      []string `json:"context"`
	LastModified *string  `json:"lastModified"`
	MoreLines    *int     `json:"moreLines,omitempty"`
}

// ---------------------------------------------------------------------------
// Field Discovery Registry
// ---------------------------------------------------------------------------

func jsonFields(t reflect.Type) []string {
	var fields []string
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		fields = append(fields, strings.Split(tag, ",")[0])
	}
	return fields
}

// ToolFieldRegistry returns a map of tool name to the JSON field names
// available for use with the `fields` parameter.
func ToolFieldRegistry() map[string][]string {
	return map[string][]string{
		"get_job_report":           jsonFields(reflect.TypeFor[JobReportRow]()),
		"get_job_runs":             jsonFields(reflect.TypeFor[JobRunRow]()),
		"get_job_run_summary":      jsonFields(reflect.TypeFor[JobRunSummary]()),
		"get_ci_test_report":       jsonFields(reflect.TypeFor[TestReportRow]()),
		"get_test_details":         jsonFields(reflect.TypeFor[TestDetailsResponse]()),
		"get_recent_test_failures": jsonFields(reflect.TypeFor[TestReportRow]()),
		"get_component_readiness":  jsonFields(reflect.TypeFor[ComponentReadinessResponse]()),
		"get_regressions":          jsonFields(reflect.TypeFor[Regression]()),
		"get_regression_detail":    {"regression", "matching_triages"},
		"get_payload_status":       jsonFields(reflect.TypeFor[ReleaseTag]()),
		"get_payload_diff":         jsonFields(reflect.TypeFor[PayloadDiffRow]()),
		"get_payload_test_failures": jsonFields(reflect.TypeFor[PayloadTestFailure]()),
		"get_releases":             jsonFields(reflect.TypeFor[ReleasesResponse]()),
		"get_release_health":       {"health", "release_health"},
		"get_pr_impact":            jsonFields(reflect.TypeFor[PRTestResult]()),
		"get_release_prs":          jsonFields(reflect.TypeFor[PullRequestRow]()),
		"search_ci_logs":           jsonFields(reflect.TypeFor[SearchResponse]()),
	}
}
