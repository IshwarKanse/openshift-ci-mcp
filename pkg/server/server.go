package server

//go:generate go run ../../cmd/gen-readme-tools

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/openshift-eng/openshift-ci-mcp/pkg/client"
	"github.com/openshift-eng/openshift-ci-mcp/pkg/tools/domain"
	"github.com/openshift-eng/openshift-ci-mcp/pkg/tools/proxy"
)

type ToolGroup struct {
	Name  string
	Desc  string
	Tools []string
}

var AllGroups = []ToolGroup{
	{Name: "core", Desc: "Release metadata and variant dimensions", Tools: []string{"get_releases", "get_release_health", "get_variants", "get_tool_fields"}},
	{Name: "payload", Desc: "Component readiness, regressions, and payload acceptance", Tools: []string{"get_component_readiness", "get_regressions", "get_regression_detail", "get_payload_status", "get_payload_diff", "get_payload_test_failures"}},
	{Name: "jobs", Desc: "CI job pass rates and run history", Tools: []string{"get_job_report", "get_job_runs", "get_job_run_summary"}},
	{Name: "tests", Desc: "Test pass/fail/flake rates and recent failures", Tools: []string{"get_ci_test_report", "get_test_details", "get_recent_test_failures"}},
	{Name: "prs", Desc: "Pull request listings and CI impact", Tools: []string{"get_release_prs", "get_pr_impact"}},
	{Name: "search", Desc: "Build log and JUnit failure search", Tools: []string{"search_ci_logs"}},
	{Name: "prow", Desc: "Prow job history, run steps, logs and artifacts", Tools: []string{"get_prow_job_history", "get_prow_job_run", "get_prow_artifacts", "get_prow_artifact_content"}},
	{Name: "proxies", Desc: "Raw passthrough to upstream APIs", Tools: []string{"sippy_api", "release_controller_api", "search_ci_api"}},
}

var DomainGroupNames = func() []string {
	var names []string
	for _, g := range AllGroups {
		if g.Name != "proxies" {
			names = append(names, g.Name)
		}
	}
	return names
}()

var AllGroupNames = func() []string {
	var names []string
	for _, g := range AllGroups {
		names = append(names, g.Name)
	}
	return names
}()

var allToolNames = func() map[string]bool {
	m := make(map[string]bool)
	for _, g := range AllGroups {
		for _, t := range g.Tools {
			m[t] = true
		}
	}
	return m
}()

var groupNames = func() map[string]bool {
	m := make(map[string]bool)
	for _, g := range AllGroups {
		m[g.Name] = true
	}
	return m
}()

// ResolveTools expands a list of group names and/or individual tool names
// into a set suitable for Config.Tools. Group names are expanded to their
// constituent tools; individual tool names are passed through as-is.
func ResolveTools(names []string) (map[string]bool, error) {
	var invalid []string
	result := make(map[string]bool)
	for _, n := range names {
		if groupNames[n] {
			result[n] = true
		} else if allToolNames[n] {
			result[n] = true
		} else {
			invalid = append(invalid, n)
		}
	}
	if len(invalid) > 0 {
		sort.Strings(invalid)
		all := make([]string, 0, len(AllGroupNames)+len(allToolNames))
		all = append(all, AllGroupNames...)
		for t := range allToolNames {
			all = append(all, t)
		}
		sort.Strings(all)
		return nil, fmt.Errorf("unknown tool group(s) or tool(s): %s; valid values: %s",
			strings.Join(invalid, ", "), strings.Join(all, ", "))
	}
	return result, nil
}

type Config struct {
	SippyURL             string
	ReleaseControllerURL string
	SearchCIURL          string
	Timeout              time.Duration
	CacheTTL             time.Duration
	Tools                map[string]bool
	ProwDecks            []client.Deck
}

func DefaultConfig() Config {
	tools := make(map[string]bool, len(DomainGroupNames))
	for _, name := range DomainGroupNames {
		tools[name] = true
	}
	return Config{
		SippyURL:             "https://sippy.dptools.openshift.org",
		ReleaseControllerURL: "https://amd64.ocp.releases.ci.openshift.org",
		SearchCIURL:          "https://search.ci.openshift.org",
		Timeout:              60 * time.Second,
		CacheTTL:             5 * time.Minute,
		Tools:                tools,
		ProwDecks:            client.DefaultDecks,
	}
}

type toolRegistration struct {
	group string
	tools []string
	fn    func()
}

func New(cfg Config) *server.MCPServer {
	httpClient := &http.Client{Timeout: cfg.Timeout}

	sippy := client.NewSippy(cfg.SippyURL, httpClient)
	rc := client.NewReleaseController(cfg.ReleaseControllerURL, httpClient)
	search := client.NewSearchCI(cfg.SearchCIURL, httpClient)
	prow := client.NewProw(cfg.ProwDecks, httpClient)
	cache := client.NewResponseCache(cfg.CacheTTL)

	s := server.NewMCPServer(
		"openshift-ci-mcp",
		"0.1.0",
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)

	registrations := []toolRegistration{
		{"core", []string{"get_releases", "get_release_health"}, func() { domain.RegisterReleaseTools(s, sippy, cache) }},
		{"core", []string{"get_variants"}, func() { domain.RegisterVariantTools(s, sippy, cache) }},
		{"core", []string{"get_tool_fields"}, func() { domain.RegisterFieldTools(s) }},
		{"payload", []string{"get_component_readiness", "get_regressions", "get_regression_detail"}, func() { domain.RegisterComponentTools(s, sippy, cache) }},
		{"payload", []string{"get_payload_status", "get_payload_diff", "get_payload_test_failures"}, func() { domain.RegisterPayloadTools(s, sippy, rc, cache) }},
		{"jobs", []string{"get_job_report", "get_job_runs", "get_job_run_summary"}, func() { domain.RegisterJobTools(s, sippy, cache) }},
		{"tests", []string{"get_ci_test_report", "get_test_details", "get_recent_test_failures"}, func() { domain.RegisterTestTools(s, sippy, cache) }},
		{"prs", []string{"get_release_prs", "get_pr_impact"}, func() { domain.RegisterPullRequestTools(s, sippy, cache) }},
		{"search", []string{"search_ci_logs"}, func() { domain.RegisterSearchTools(s, search) }},
		{"prow", []string{"get_prow_job_history", "get_prow_job_run", "get_prow_artifacts", "get_prow_artifact_content"}, func() { domain.RegisterProwTools(s, prow, cache) }},
		{"proxies", []string{"sippy_api"}, func() { proxy.RegisterSippyProxy(s, sippy) }},
		{"proxies", []string{"release_controller_api"}, func() { proxy.RegisterReleaseControllerProxy(s, rc) }},
		{"proxies", []string{"search_ci_api"}, func() { proxy.RegisterSearchCIProxy(s, search) }},
	}

	for _, reg := range registrations {
		if cfg.Tools[reg.group] {
			reg.fn()
			continue
		}
		for _, tool := range reg.tools {
			if cfg.Tools[tool] {
				reg.fn()
				break
			}
		}
	}

	return s
}
