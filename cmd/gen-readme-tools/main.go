package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	mcpserver "github.com/mark3labs/mcp-go/server"

	server "github.com/openshift-eng/openshift-ci-mcp/pkg/server"
	"github.com/openshift-eng/openshift-ci-mcp/pkg/tools/domain"
	"github.com/openshift-eng/openshift-ci-mcp/pkg/tools/proxy"
)

func main() {
	root := findProjectRoot()
	readmePath := filepath.Join(root, "README.md")

	readme, err := os.ReadFile(readmePath)
	if err != nil {
		fatal("reading README.md: %v", err)
	}

	readme = replaceSection(readme, "DOMAIN TOOLS", renderToolsTable(buildDomainServer()))
	readme = replaceSection(readme, "PROXY TOOLS", renderToolsTable(buildProxyServer()))
	readme = replaceSection(readme, "TOOL GROUPS", renderGroupsTable())

	if err := os.WriteFile(readmePath, readme, 0o644); err != nil {
		fatal("writing README.md: %v", err)
	}
}

func buildDomainServer() *mcpserver.MCPServer {
	s := mcpserver.NewMCPServer("gen", "0.0.0")
	var sippy nopSippy
	var rc nopRC
	var search nopSearch

	domain.RegisterReleaseTools(s, sippy, nil)
	domain.RegisterVariantTools(s, sippy, nil)
	domain.RegisterJobTools(s, sippy, nil)
	domain.RegisterTestTools(s, sippy, nil)
	domain.RegisterComponentTools(s, sippy, nil)
	domain.RegisterPayloadTools(s, sippy, rc, nil)
	domain.RegisterSearchTools(s, search)
	domain.RegisterPullRequestTools(s, sippy, nil)
	domain.RegisterProwTools(s, nopProw{}, nil)

	return s
}

func buildProxyServer() *mcpserver.MCPServer {
	s := mcpserver.NewMCPServer("gen", "0.0.0")
	var sippy nopSippy
	var rc nopRC
	var search nopSearch

	proxy.RegisterSippyProxy(s, sippy)
	proxy.RegisterReleaseControllerProxy(s, rc)
	proxy.RegisterSearchCIProxy(s, search)

	return s
}

func renderToolsTable(srv *mcpserver.MCPServer) string {
	toolToGroup := make(map[string]string)
	groupOrder := make(map[string]int)
	for i, g := range server.AllGroups {
		groupOrder[g.Name] = i
		for _, t := range g.Tools {
			toolToGroup[t] = g.Name
		}
	}

	tools := srv.ListTools()
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		gi, gj := groupOrder[toolToGroup[names[i]]], groupOrder[toolToGroup[names[j]]]
		if gi != gj {
			return gi < gj
		}
		return names[i] < names[j]
	})

	var buf strings.Builder
	buf.WriteString("| Tool | Group | Description |\n")
	buf.WriteString("| ---- | ----- | ----------- |\n")
	for _, name := range names {
		desc := strings.ReplaceAll(tools[name].Tool.Description, "|", "\\|")
		fmt.Fprintf(&buf, "| `%s` | `%s` | %s |\n", name, toolToGroup[name], desc)
	}
	return buf.String()
}

func renderGroupsTable() string {
	var buf strings.Builder
	buf.WriteString("| Group | Description | Tools |\n")
	buf.WriteString("| ----- | ----------- | ----- |\n")
	for _, g := range server.AllGroups {
		tools := make([]string, len(g.Tools))
		for i, t := range g.Tools {
			tools[i] = "`" + t + "`"
		}
		fmt.Fprintf(&buf, "| `%s` | %s | %s |\n", g.Name, g.Desc, strings.Join(tools, ", "))
	}
	return buf.String()
}

func replaceSection(content []byte, marker, replacement string) []byte {
	begin := []byte(fmt.Sprintf("<!-- BEGIN %s -->", marker))
	end := []byte(fmt.Sprintf("<!-- END %s -->", marker))

	startIdx := bytes.Index(content, begin)
	if startIdx == -1 {
		fatal("marker %q not found in README.md", string(begin))
	}
	endIdx := bytes.Index(content[startIdx:], end)
	if endIdx == -1 {
		fatal("marker %q not found in README.md", string(end))
	}
	endIdx += startIdx

	var buf bytes.Buffer
	buf.Write(content[:startIdx])
	buf.Write(begin)
	buf.WriteByte('\n')
	buf.WriteString(replacement)
	buf.Write(end)
	buf.Write(content[endIdx+len(end):])

	return buf.Bytes()
}

func findProjectRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		fatal("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			fatal("could not find project root (no go.mod found)")
		}
		dir = parent
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen-readme-tools: "+format+"\n", args...)
	os.Exit(1)
}
