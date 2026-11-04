package domain_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openshift-eng/openshift-ci-mcp/pkg/tools/domain"
)

func TestGetJobReport(t *testing.T) {
	mock := newMockSippy(map[string][]byte{
		"/api/jobs": []byte(`{"rows":[{"name":"periodic-ci-e2e-aws","current_pass_percentage":95.5}],"total_rows":1}`),
	})

	handler := domain.GetJobReportHandler(mock, nil)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"release": "4.18",
		"arch":    "amd64",
	}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}
}

func TestGetJobReport_VariantFiltering(t *testing.T) {
	var capturedParams map[string]string
	mock := &capturingSippy{
		response: []byte(`{"rows":[],"total_rows":0}`),
		onGet: func(path string, params map[string]string) {
			capturedParams = params
		},
	}

	handler := domain.GetJobReportHandler(mock, nil)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"release":  "4.18",
		"arch":     "arm64",
		"topology": "single",
	}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}
	if !strings.Contains(capturedParams["filter"], "Architecture:arm64") {
		t.Error("expected filter to contain Architecture:arm64")
	}
	if !strings.Contains(capturedParams["filter"], "Topology:single") {
		t.Error("expected filter to contain Topology:single")
	}
}

func TestGetJobReport_SortParams(t *testing.T) {
	var capturedParams map[string]string
	mock := &capturingSippy{
		response: []byte(`[{"name":"job1","current_pass_percentage":95.5}]`),
		onGet: func(path string, params map[string]string) {
			capturedParams = params
		},
	}

	handler := domain.GetJobReportHandler(mock, nil)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"release":    "4.18",
		"sort_field": "net_improvement",
		"sort_order": "desc",
	}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}
	if capturedParams["sortField"] != "net_improvement" {
		t.Errorf("expected sortField=net_improvement, got %s", capturedParams["sortField"])
	}
	if capturedParams["sort"] != "desc" {
		t.Errorf("expected sort=desc, got %s", capturedParams["sort"])
	}
}

func TestGetJobReport_ResponseTrimming(t *testing.T) {
	mock := newMockSippy(map[string][]byte{
		"/api/jobs": []byte(`[{"name":"job1","id":99,"brief_name":"job1","current_pass_percentage":95.5,"current_runs":100,"current_fails":5,"previous_pass_percentage":90.0,"previous_runs":80,"net_improvement":5.5,"open_bugs":1,"current_projected_pass_percentage":96.0,"test_grid_url":"http://example.com"}]`),
	})

	handler := domain.GetJobReportHandler(mock, nil)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"release": "4.18"}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if strings.Contains(text, "test_grid_url") {
		t.Error("expected test_grid_url to be trimmed")
	}
	if !strings.Contains(text, "current_pass_percentage") {
		t.Error("expected current_pass_percentage to be kept")
	}
	if !strings.Contains(text, "current_projected_pass_percentage") {
		t.Error("expected current_projected_pass_percentage to be kept")
	}
}

func TestGetJobRuns(t *testing.T) {
	mock := newMockSippy(map[string][]byte{
		"/api/jobs/runs": []byte(`{"rows":[{"prow_id":"2116944951399944192","job":"test-job","url":"https://prow.ci/1","test_failures":0,"succeeded":true,"timestamp":"2026-11-01T17:26:15Z","overall_result":"S"}],"total_rows":1,"page_size":10,"page":1}`),
	})

	handler := domain.GetJobRunsHandler(mock, nil)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"release":  "4.18",
		"job_name": "periodic-ci-e2e-aws",
	}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}
}

func TestGetJobRuns_PageParam(t *testing.T) {
	var capturedParams map[string]string
	mock := &capturingSippy{
		response: []byte(`{"rows":[],"total_rows":0,"page_size":10,"page":2}`),
		onGet: func(path string, params map[string]string) {
			capturedParams = params
		},
	}

	handler := domain.GetJobRunsHandler(mock, nil)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"release":  "4.18",
		"job_name": "test-job",
		"page":     float64(2),
	}
	_, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedParams["page"] != "2" {
		t.Errorf("expected page=2, got %s", capturedParams["page"])
	}
}

func TestGetJobRuns_ResponseTrimming(t *testing.T) {
	mock := newMockSippy(map[string][]byte{
		"/api/jobs/runs": []byte(`{"rows":[{"id":2116944951399944192,"brief_name":"job-1","tags":null,"test_grid_url":"https://prow.ci/view/1","prow_id":"2116944951399944192","job":"periodic-ci-job-1","cluster":"build05","url":"https://prow.ci/1","test_flakes":1,"flaked_test_names":null,"test_failures":3,"failed":false,"infrastructure_failure":false,"known_failure":false,"succeeded":true,"timestamp":"2026-11-01T17:26:15Z","overall_result":"S","pull_request_org":"","pull_request_repo":"","pull_request_link":"","pull_request_sha":"","pull_request_author":"","labels":null}],"page_size":10,"page":1,"total_rows":1}`),
	})

	handler := domain.GetJobRunsHandler(mock, nil)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"release":  "4.18",
		"job_name": "job-1",
	}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := result.Content[0].(mcp.TextContent).Text
	if strings.Contains(text, `"id"`) {
		t.Error("expected id to be trimmed")
	}
	if strings.Contains(text, "cluster") {
		t.Error("expected cluster to be trimmed")
	}
	if strings.Contains(text, "pull_request_org") {
		t.Error("expected pull_request_org to be trimmed")
	}
	if strings.Contains(text, "test_grid_url") {
		t.Error("expected test_grid_url to be trimmed")
	}
	if !strings.Contains(text, `"prow_id":"2116944951399944192"`) {
		t.Error("expected prow_id to be kept as the exact string ID")
	}
	if !strings.Contains(text, "prow_id") {
		t.Error("expected prow_id to be kept")
	}
	if !strings.Contains(text, `"job"`) {
		t.Error("expected job to be kept")
	}
	if !strings.Contains(text, "brief_name") {
		t.Error("expected brief_name to be kept")
	}
}

func TestGetJobRunSummary(t *testing.T) {
	mock := newMockSippy(map[string][]byte{
		"/api/job/run/summary": []byte(`{"id":12345,"name":"e2e-aws","succeeded":true}`),
	})

	handler := domain.GetJobRunSummaryHandler(mock, nil)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"prow_job_run_id": "12345",
	}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}
}
