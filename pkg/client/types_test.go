package client

import (
	"encoding/json"
	"testing"
)

func TestReshapeJSON_DropsExtraFields(t *testing.T) {
	input := `{"name":"test-job","id":99,"brief_name":"test-job","current_pass_percentage":85.5,"current_runs":100,"current_fails":15,"previous_pass_percentage":80.0,"previous_runs":90,"net_improvement":5.5,"open_bugs":2,"current_projected_pass_percentage":86.0,"test_grid_url":"http://example.com"}`

	out, err := ReshapeJSON[[]JobReportRow]([]byte("[" + input + "]"))
	if err != nil {
		t.Fatalf("ReshapeJSON failed: %v", err)
	}

	var result []map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 row, got %d", len(result))
	}

	row := result[0]
	if _, ok := row["id"]; ok {
		t.Error("expected 'id' to be dropped")
	}
	if _, ok := row["brief_name"]; !ok {
		t.Error("expected 'brief_name' to be kept")
	}
	if _, ok := row["test_grid_url"]; ok {
		t.Error("expected 'test_grid_url' to be dropped")
	}
	if _, ok := row["current_projected_pass_percentage"]; !ok {
		t.Error("expected 'current_projected_pass_percentage' to be kept")
	}
	if row["name"] != "test-job" {
		t.Errorf("expected name='test-job', got %v", row["name"])
	}
}

func TestReshapeJSON_JobRunsResponse(t *testing.T) {
	input := `{
		"rows": [
			{
				"id": 1,
				"prow_id": 1,
				"brief_name": "job-1",
				"job": "periodic-ci-job-1",
				"url": "https://prow.ci/1",
				"test_failures": 3,
				"test_flakes": 1,
				"failed": false,
				"infrastructure_failure": false,
				"known_failure": false,
				"succeeded": true,
				"timestamp": 1700000000,
				"overall_result": "S",
				"cluster": "build05",
				"pull_request_org": "",
				"pull_request_repo": "",
				"tags": null
			}
		],
		"page_size": 25,
		"page": 1,
		"total_rows": 1
	}`

	out, err := ReshapeJSON[JobRunsResponse]([]byte(input))
	if err != nil {
		t.Fatalf("ReshapeJSON failed: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	rows := result["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0].(map[string]any)

	for _, dropped := range []string{"id", "cluster", "pull_request_org", "pull_request_repo", "tags"} {
		if _, ok := row[dropped]; ok {
			t.Errorf("expected '%s' to be dropped", dropped)
		}
	}
	for _, kept := range []string{"prow_id", "job", "brief_name", "url", "test_failures", "succeeded", "overall_result"} {
		if _, ok := row[kept]; !ok {
			t.Errorf("expected '%s' to be kept", kept)
		}
	}

	if result["total_rows"].(float64) != 1 {
		t.Error("expected total_rows to be preserved")
	}
}

func TestReshapeJSON_TestReportRow(t *testing.T) {
	input := `[{
		"name": "test-1",
		"id": 42,
		"suite_name": "suite-a",
		"jira_component": "Networking",
		"jira_component_id": 99,
		"current_pass_percentage": 95.0,
		"current_runs": 200,
		"current_successes": 190,
		"current_failures": 5,
		"current_flakes": 5,
		"current_failure_percentage": 2.5,
		"current_flake_percentage": 2.5,
		"current_working_percentage": 97.5,
		"previous_successes": 180,
		"previous_failures": 10,
		"previous_flakes": 10,
		"previous_pass_percentage": 90.0,
		"previous_runs": 200,
		"net_improvement": 5.0,
		"net_failure_improvement": 2.5,
		"net_flake_improvement": 2.5,
		"open_bugs": 1
	}]`

	out, err := ReshapeJSON[[]TestReportRow]([]byte(input))
	if err != nil {
		t.Fatalf("ReshapeJSON failed: %v", err)
	}

	var result []map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	row := result[0]
	for _, dropped := range []string{"id", "jira_component_id"} {
		if _, ok := row[dropped]; ok {
			t.Errorf("expected '%s' to be dropped from top level", dropped)
		}
	}
	for _, kept := range []string{"name", "suite_name", "jira_component", "current_pass_percentage", "current_runs", "net_improvement"} {
		if _, ok := row[kept]; !ok {
			t.Errorf("expected '%s' to be kept at top level", kept)
		}
	}
	// Verify metrics sub-object contains the nested fields
	metrics, ok := row["metrics"].(map[string]any)
	if !ok {
		t.Fatal("expected 'metrics' sub-object")
	}
	for _, nested := range []string{
		"current_failure_percentage", "current_flake_percentage", "current_working_percentage",
		"previous_successes", "previous_failures", "previous_flakes",
		"previous_pass_percentage", "previous_runs",
		"net_failure_improvement", "net_flake_improvement",
	} {
		if _, ok := metrics[nested]; !ok {
			t.Errorf("expected '%s' inside metrics sub-object", nested)
		}
	}
	// Verify those fields are NOT at the top level
	for _, nested := range []string{"current_failure_percentage", "previous_successes", "net_failure_improvement"} {
		if _, ok := row[nested]; ok {
			t.Errorf("expected '%s' to be nested under metrics, not at top level", nested)
		}
	}
}

func TestPaginateArray_Page1(t *testing.T) {
	input := `[{"id":1},{"id":2},{"id":3},{"id":4},{"id":5}]`
	out, err := PaginateArray([]byte(input), 2, 1)
	if err != nil {
		t.Fatalf("PaginateArray failed: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["total_rows"].(float64) != 5 {
		t.Errorf("expected total_rows=5, got %v", result["total_rows"])
	}
	if result["page"].(float64) != 1 {
		t.Errorf("expected page=1, got %v", result["page"])
	}
	if result["page_size"].(float64) != 2 {
		t.Errorf("expected page_size=2, got %v", result["page_size"])
	}
	rows := result["rows"].([]any)
	if len(rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(rows))
	}
}

func TestPaginateArray_Page2(t *testing.T) {
	input := `[{"id":1},{"id":2},{"id":3},{"id":4},{"id":5}]`
	out, err := PaginateArray([]byte(input), 2, 2)
	if err != nil {
		t.Fatalf("PaginateArray failed: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	rows := result["rows"].([]any)
	if len(rows) != 2 {
		t.Errorf("expected 2 rows on page 2, got %d", len(rows))
	}
	first := rows[0].(map[string]any)
	if first["id"].(float64) != 3 {
		t.Errorf("expected first row id=3, got %v", first["id"])
	}
}

func TestPaginateArray_BeyondRange(t *testing.T) {
	input := `[{"id":1},{"id":2}]`
	out, err := PaginateArray([]byte(input), 10, 5)
	if err != nil {
		t.Fatalf("PaginateArray failed: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	rows := result["rows"].([]any)
	if len(rows) != 0 {
		t.Errorf("expected 0 rows beyond range, got %d", len(rows))
	}
	if result["total_rows"].(float64) != 2 {
		t.Errorf("expected total_rows=2, got %v", result["total_rows"])
	}
}

func TestPaginateArray_Empty(t *testing.T) {
	out, err := PaginateArray([]byte("[]"), 25, 1)
	if err != nil {
		t.Fatalf("PaginateArray failed: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["total_rows"].(float64) != 0 {
		t.Errorf("expected total_rows=0, got %v", result["total_rows"])
	}
}

func TestFilterFields_Array(t *testing.T) {
	input := `[{"name":"job1","current_pass_percentage":95.5,"current_runs":100,"open_bugs":2}]`
	out, err := FilterFields([]byte(input), "name,current_pass_percentage")
	if err != nil {
		t.Fatalf("FilterFields failed: %v", err)
	}
	var result []map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	row := result[0]
	if row["name"] != "job1" {
		t.Error("expected name to be kept")
	}
	if _, ok := row["current_pass_percentage"]; !ok {
		t.Error("expected current_pass_percentage to be kept")
	}
	if _, ok := row["current_runs"]; ok {
		t.Error("expected current_runs to be filtered out")
	}
	if _, ok := row["open_bugs"]; ok {
		t.Error("expected open_bugs to be filtered out")
	}
}

func TestFilterFields_WrappedRows(t *testing.T) {
	input := `{"rows":[{"name":"job1","url":"http://x","cluster":"build05"}],"total_rows":1,"page":1}`
	out, err := FilterFields([]byte(input), "name,url")
	if err != nil {
		t.Fatalf("FilterFields failed: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["total_rows"].(float64) != 1 {
		t.Error("expected wrapper fields to be preserved")
	}
	rows := result["rows"].([]any)
	row := rows[0].(map[string]any)
	if _, ok := row["name"]; !ok {
		t.Error("expected name to be kept in row")
	}
	if _, ok := row["cluster"]; ok {
		t.Error("expected cluster to be filtered from row")
	}
}

func TestFilterFields_PreservesLargeIntegers(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "array",
			input: `[{"id":2112233877102661632,"job":"j","cluster":"build07"}]`,
			want:  `[{"id":2112233877102661632,"job":"j"}]`,
		},
		{
			name:  "wrapped rows",
			input: `{"rows":[{"id":2112233877102661632,"job":"j","cluster":"build07"}],"page":1}`,
			want:  `{"page":1,"rows":[{"id":2112233877102661632,"job":"j"}]}`,
		},
		{
			name:  "object",
			input: `{"id":2112233877102661632,"job":"j","cluster":"build07"}`,
			want:  `{"id":2112233877102661632,"job":"j"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := FilterFields([]byte(tt.input), "id,job")
			if err != nil {
				t.Fatalf("FilterFields failed: %v", err)
			}
			if string(out) != tt.want {
				t.Errorf("got %s, want %s", out, tt.want)
			}
		})
	}
}

func TestFilterFields_TrailingDataUnchanged(t *testing.T) {
	input := `[{"id":1}] trailing`
	out, err := FilterFields([]byte(input), "id")
	if err != nil {
		t.Fatalf("FilterFields failed: %v", err)
	}
	if string(out) != input {
		t.Errorf("expected invalid JSON to be returned unchanged, got %s", out)
	}
}

func TestFilterFields_EmptyString(t *testing.T) {
	input := `[{"name":"x","id":1}]`
	out, err := FilterFields([]byte(input), "")
	if err != nil {
		t.Fatalf("FilterFields failed: %v", err)
	}
	if string(out) != input {
		t.Error("expected no filtering when fields is empty")
	}
}

func TestToolFieldRegistry(t *testing.T) {
	reg := ToolFieldRegistry()
	if len(reg) != 17 {
		t.Errorf("expected 17 tools in registry, got %d", len(reg))
	}
	fields := reg["get_job_report"]
	if len(fields) == 0 {
		t.Fatal("expected fields for get_job_report")
	}
	hasName := false
	for _, f := range fields {
		if f == "name" {
			hasName = true
		}
	}
	if !hasName {
		t.Errorf("expected 'name' in get_job_report fields, got %v", fields)
	}
	if _, ok := reg["get_release_health"]; !ok {
		t.Error("expected get_release_health in registry")
	}
}

func TestReshapeJSON_EmptyInput(t *testing.T) {
	out, err := ReshapeJSON[[]JobReportRow]([]byte("[]"))
	if err != nil {
		t.Fatalf("ReshapeJSON failed: %v", err)
	}
	if string(out) != "[]" {
		t.Errorf("expected '[]', got '%s'", string(out))
	}
}

func TestReshapeJSON_InvalidJSON(t *testing.T) {
	_, err := ReshapeJSON[[]JobReportRow]([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
