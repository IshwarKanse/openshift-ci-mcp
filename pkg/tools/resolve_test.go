package tools_test

import (
	"context"
	"testing"

	"github.com/openshift-eng/openshift-ci-mcp/pkg/tools"
)

type mockSippy struct {
	response []byte
	err      error
}

func (m *mockSippy) Get(ctx context.Context, path string, params map[string]string) ([]byte, error) {
	return m.response, m.err
}

func TestResolveRelease_ExplicitRelease(t *testing.T) {
	mock := &mockSippy{}
	release, err := tools.ResolveRelease(context.Background(), mock, "4.18")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if release != "4.18" {
		t.Errorf("expected '4.18', got %q", release)
	}
}

func TestResolveRelease_DefaultToCurrentDev(t *testing.T) {
	mock := &mockSippy{
		response: []byte(`{
			"releases": ["4.19", "4.18", "4.17"],
			"dates": {
				"4.19": {},
				"4.18": {"ga": "2025-06-15T00:00:00Z"},
				"4.17": {"ga": "2024-12-15T00:00:00Z"}
			}
		}`),
	}
	release, err := tools.ResolveRelease(context.Background(), mock, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if release != "4.19" {
		t.Errorf("expected '4.19' (no GA date = in development), got %q", release)
	}
}

func TestResolveRelease_AllGA_FallbackToLatest(t *testing.T) {
	mock := &mockSippy{
		response: []byte(`{
			"releases": ["4.18", "4.17"],
			"dates": {
				"4.18": {"ga": "2025-06-15T00:00:00Z"},
				"4.17": {"ga": "2024-12-15T00:00:00Z"}
			}
		}`),
	}
	release, err := tools.ResolveRelease(context.Background(), mock, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if release != "4.18" {
		t.Errorf("expected '4.18' (latest GA), got %q", release)
	}
}

func TestResolveRelease_SkipsNonOCPReleases(t *testing.T) {
	mock := &mockSippy{
		response: []byte(`{
			"releases": ["gcp-hcp-main", "quay-3.18", "mcp-0.5", "5.1", "5.1-okd", "4.23", "5.0", "4.22", "4.22-okd"],
			"dates": {
				"gcp-hcp-main": {"development_start": "2026-09-17"},
				"quay-3.18": {"development_start": "2026-09-02"},
				"mcp-0.5": {"development_start": "2026-08-19"},
				"5.1": {"development_start": "2026-08-14"},
				"5.1-okd": {"development_start": "2026-08-14"},
				"4.23": {"development_start": "2026-08-13"},
				"5.0": {"development_start": "2026-04-13"},
				"4.22": {"ga": "2026-06-09", "development_start": "2025-12-12"},
				"4.22-okd": {"development_start": "2025-12-11"}
			}
		}`),
	}
	release, err := tools.ResolveRelease(context.Background(), mock, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if release != "5.1" {
		t.Errorf("expected '5.1' (first OCP release without GA), got %q", release)
	}
}

func TestResolveRelease_OCPReleaseWithoutDates(t *testing.T) {
	mock := &mockSippy{
		response: []byte(`{
			"releases": ["gcp-hcp-main", "4.19", "4.18"],
			"dates": {
				"4.18": {"ga": "2025-06-15T00:00:00Z"}
			}
		}`),
	}
	release, err := tools.ResolveRelease(context.Background(), mock, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if release != "4.19" {
		t.Errorf("expected '4.19' (no dates entry = in development), got %q", release)
	}
}

func TestResolveRelease_AllOCPGA_SkipsNonOCPReleases(t *testing.T) {
	mock := &mockSippy{
		response: []byte(`{
			"releases": ["gcp-hcp-main", "4.18", "4.17"],
			"dates": {
				"gcp-hcp-main": {},
				"4.18": {"ga": "2025-06-15T00:00:00Z"},
				"4.17": {"ga": "2024-12-15T00:00:00Z"}
			}
		}`),
	}
	release, err := tools.ResolveRelease(context.Background(), mock, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if release != "4.18" {
		t.Errorf("expected '4.18' (latest OCP GA), got %q", release)
	}
}

func TestResolveRelease_NoOCPReleases_FallsBackToAll(t *testing.T) {
	mock := &mockSippy{
		response: []byte(`{
			"releases": ["quay-io", "gcp-hcp-main"],
			"dates": {
				"quay-io": {"ga": "2025-06-15T00:00:00Z"},
				"gcp-hcp-main": {}
			}
		}`),
	}
	release, err := tools.ResolveRelease(context.Background(), mock, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if release != "gcp-hcp-main" {
		t.Errorf("expected 'gcp-hcp-main', got %q", release)
	}
}
