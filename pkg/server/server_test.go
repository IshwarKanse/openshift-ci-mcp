package server

import (
	"testing"
)

func TestNew_ToolGroupGating(t *testing.T) {
	tests := []struct {
		name    string
		tools   map[string]bool
		wantLen int
		wantHas []string
		wantNot []string
	}{
		{
			name:    "default config enables all domain tools",
			tools:   DefaultConfig().Tools,
			wantLen: 23,
			wantHas: []string{"get_releases", "get_job_report", "get_ci_test_report", "get_component_readiness", "get_release_prs", "search_ci_logs", "get_tool_fields", "get_prow_job_history"},
			wantNot: []string{"sippy_api", "release_controller_api", "search_ci_api"},
		},
		{
			name:    "empty map registers nothing",
			tools:   map[string]bool{},
			wantLen: 0,
		},
		{
			name:    "core only",
			tools:   map[string]bool{"core": true},
			wantLen: 4,
			wantHas: []string{"get_releases", "get_release_health", "get_variants", "get_tool_fields"},
			wantNot: []string{"get_job_report", "get_component_readiness"},
		},
		{
			name:    "jobs only",
			tools:   map[string]bool{"jobs": true},
			wantLen: 3,
			wantHas: []string{"get_job_report", "get_job_runs", "get_job_run_summary"},
		},
		{
			name:    "tests only",
			tools:   map[string]bool{"tests": true},
			wantLen: 3,
			wantHas: []string{"get_ci_test_report", "get_test_details", "get_recent_test_failures"},
		},
		{
			name:    "payload only",
			tools:   map[string]bool{"payload": true},
			wantLen: 6,
			wantHas: []string{"get_component_readiness", "get_regressions", "get_regression_detail", "get_payload_status", "get_payload_diff", "get_payload_test_failures"},
		},
		{
			name:    "prs only",
			tools:   map[string]bool{"prs": true},
			wantLen: 2,
			wantHas: []string{"get_release_prs", "get_pr_impact"},
		},
		{
			name:    "search only",
			tools:   map[string]bool{"search": true},
			wantLen: 1,
			wantHas: []string{"search_ci_logs"},
		},
		{
			name:    "prow only",
			tools:   map[string]bool{"prow": true},
			wantLen: 4,
			wantHas: []string{"get_prow_job_history", "get_prow_job_run", "get_prow_artifacts", "get_prow_artifact_content"},
			wantNot: []string{"get_job_runs"},
		},
		{
			name:    "proxies group",
			tools:   map[string]bool{"proxies": true},
			wantLen: 3,
			wantHas: []string{"sippy_api", "release_controller_api", "search_ci_api"},
			wantNot: []string{"get_releases"},
		},
		{
			name:    "individual proxy tool",
			tools:   map[string]bool{"sippy_api": true},
			wantLen: 1,
			wantHas: []string{"sippy_api"},
			wantNot: []string{"release_controller_api", "search_ci_api"},
		},
		{
			name:    "individual tool from domain group",
			tools:   map[string]bool{"get_releases": true},
			wantLen: 2,
			wantHas: []string{"get_releases", "get_release_health"},
			wantNot: []string{"get_variants"},
		},
		{
			name:    "group plus individual tool from another group",
			tools:   map[string]bool{"core": true, "sippy_api": true},
			wantLen: 5,
			wantHas: []string{"get_releases", "get_variants", "get_tool_fields", "sippy_api"},
			wantNot: []string{"release_controller_api"},
		},
		{
			name:    "core plus proxies",
			tools:   map[string]bool{"core": true, "proxies": true},
			wantLen: 7,
			wantHas: []string{"get_releases", "sippy_api"},
		},
		{
			name:    "all groups",
			tools:   map[string]bool{"core": true, "payload": true, "jobs": true, "tests": true, "prs": true, "search": true, "prow": true, "proxies": true},
			wantLen: 26,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Tools = tt.tools

			s := New(cfg)
			tools := s.ListTools()

			if got := len(tools); got != tt.wantLen {
				var names []string
				for _, tool := range tools {
					names = append(names, tool.Tool.Name)
				}
				t.Errorf("got %d tools %v, want %d", got, names, tt.wantLen)
			}

			toolSet := make(map[string]bool, len(tools))
			for _, tool := range tools {
				toolSet[tool.Tool.Name] = true
			}

			for _, name := range tt.wantHas {
				if !toolSet[name] {
					t.Errorf("expected tool %q to be registered", name)
				}
			}
			for _, name := range tt.wantNot {
				if toolSet[name] {
					t.Errorf("tool %q should not be registered", name)
				}
			}
		})
	}
}

func TestResolveTools(t *testing.T) {
	tests := []struct {
		name    string
		input   []string
		wantHas []string
		wantErr bool
	}{
		{"group name", []string{"core"}, []string{"core"}, false},
		{"tool name", []string{"sippy_api"}, []string{"sippy_api"}, false},
		{"mixed", []string{"core", "sippy_api"}, []string{"core", "sippy_api"}, false},
		{"invalid", []string{"core", "bogus"}, nil, true},
		{"empty", []string{}, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ResolveTools(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveTools(%v) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			for _, name := range tt.wantHas {
				if !result[name] {
					t.Errorf("expected %q in result %v", name, result)
				}
			}
		})
	}
}

func TestDefaultConfig_ContainsDomainGroups(t *testing.T) {
	cfg := DefaultConfig()
	for _, name := range DomainGroupNames {
		if !cfg.Tools[name] {
			t.Errorf("DefaultConfig().Tools missing domain group %q", name)
		}
	}
	if cfg.Tools["proxies"] {
		t.Error("DefaultConfig().Tools should not include proxies")
	}
}
