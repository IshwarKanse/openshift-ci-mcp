package domain_test

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/openshift-eng/openshift-ci-mcp/pkg/tools/domain"
)

type mockSearchCI struct {
	response []byte
}

func (m *mockSearchCI) Search(ctx context.Context, query string, params map[string]string) ([]byte, error) {
	return m.response, nil
}

func (m *mockSearchCI) Get(ctx context.Context, path string, params map[string]string) ([]byte, error) {
	return m.response, nil
}

type capturingSearchCI struct {
	response []byte
	onSearch func(query string, params map[string]string)
}

func (m *capturingSearchCI) Search(ctx context.Context, query string, params map[string]string) ([]byte, error) {
	if m.onSearch != nil {
		m.onSearch(query, params)
	}
	return m.response, nil
}

func (m *capturingSearchCI) Get(ctx context.Context, path string, params map[string]string) ([]byte, error) {
	return m.response, nil
}

func TestSearchCILogs(t *testing.T) {
	mock := &mockSearchCI{response: []byte(`{"results":{"test-failure":{"matches":[{"context":["job-1"]}]}}}`)}
	handler := domain.SearchCILogsHandler(mock)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"query": "test-failure"}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}
}

func TestSearchCILogs_LimitParam(t *testing.T) {
	var capturedParams map[string]string
	mock := &capturingSearchCI{
		response: []byte(`{}`),
		onSearch: func(query string, params map[string]string) {
			capturedParams = params
		},
	}
	handler := domain.SearchCILogsHandler(mock)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"query": "test-failure",
		"limit": float64(50),
	}
	_, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedParams["maxResults"] != "50" {
		t.Errorf("expected maxResults=50, got %s", capturedParams["maxResults"])
	}
}

func TestSearchCILogs_MaxAge(t *testing.T) {
	tests := []struct {
		maxAge string
		want   string
	}{
		{"7d", "168h"},
		{"1d", "24h"},
		{"24h", "24h"},
		{"90m", "90m"},
		{"1d12h", "1d12h"},
	}
	for _, tt := range tests {
		t.Run(tt.maxAge, func(t *testing.T) {
			var capturedParams map[string]string
			mock := &capturingSearchCI{
				response: []byte(`{}`),
				onSearch: func(query string, params map[string]string) {
					capturedParams = params
				},
			}
			handler := domain.SearchCILogsHandler(mock)
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]any{
				"query":   "test-failure",
				"max_age": tt.maxAge,
			}
			if _, err := handler(context.Background(), req); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if capturedParams["maxAge"] != tt.want {
				t.Errorf("expected maxAge=%s, got %s", tt.want, capturedParams["maxAge"])
			}
		})
	}
}

func TestSearchCILogs_MaxAgeTooLarge(t *testing.T) {
	for _, maxAge := range []string{"106752d", "99999999999999999999d"} {
		t.Run(maxAge, func(t *testing.T) {
			called := false
			mock := &capturingSearchCI{
				response: []byte(`{}`),
				onSearch: func(query string, params map[string]string) { called = true },
			}
			handler := domain.SearchCILogsHandler(mock)
			req := mcp.CallToolRequest{}
			req.Params.Arguments = map[string]any{"query": "q", "max_age": maxAge}
			result, err := handler(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.IsError {
				t.Error("expected a tool error for an out-of-range day count")
			}
			if called {
				t.Error("search should not be called")
			}
		})
	}
}
