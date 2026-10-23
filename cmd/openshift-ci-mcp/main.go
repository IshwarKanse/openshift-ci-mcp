package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/openshift-eng/openshift-ci-mcp/pkg/client"
	mcpserver "github.com/openshift-eng/openshift-ci-mcp/pkg/server"
)

func main() {
	transport := flag.String("transport", "stdio", "Transport mode: stdio or http")
	port := flag.Int("port", 8080, "HTTP port (only used with --transport http)")
	timeout := flag.Duration("timeout", 60*time.Second, "Upstream request timeout")
	toolGroups := flag.String("tools", "",
		"Comma-separated tool groups or individual tool names to enable (default: all domain groups)")
	cacheTTL := flag.Duration("cache-ttl", 5*time.Minute, "TTL for client-side response cache")
	enableProxyTools := flag.Bool("enable-proxy-tools", false,
		"Add proxy tools on top of the active tool groups")
	flag.Parse()

	raw := *toolGroups
	if raw == "" {
		raw = os.Getenv("MCP_TOOLS")
	}

	cfg := mcpserver.DefaultConfig()
	cfg.Timeout = *timeout
	cfg.CacheTTL = *cacheTTL

	if raw != "" {
		tools, err := parseTools(raw)
		if err != nil {
			log.Fatal(err)
		}
		cfg.Tools = tools
	}

	if *enableProxyTools || envBool("ENABLE_PROXY_TOOLS") {
		cfg.Tools["proxies"] = true
	}

	if v := os.Getenv("SIPPY_URL"); v != "" {
		cfg.SippyURL = v
	}
	if v := os.Getenv("RELEASE_CONTROLLER_URL"); v != "" {
		cfg.ReleaseControllerURL = v
	}
	if v := os.Getenv("SEARCH_CI_URL"); v != "" {
		cfg.SearchCIURL = v
	}
	if v := os.Getenv("PROW_DECKS"); v != "" {
		decks, err := client.ParseDecks(v)
		if err != nil {
			log.Fatalf("PROW_DECKS: %v", err)
		}
		cfg.ProwDecks = client.MergeDecks(cfg.ProwDecks, decks)
	}

	s := mcpserver.New(cfg)

	switch *transport {
	case "stdio":
		if err := server.ServeStdio(s); err != nil {
			fmt.Fprintf(os.Stderr, "server error: %v\n", err)
			os.Exit(1)
		}
	case "http":
		addr := fmt.Sprintf(":%d", *port)
		sseServer := server.NewSSEServer(s,
			server.WithBaseURL(fmt.Sprintf("http://localhost:%d", *port)),
		)
		log.Printf("Starting HTTP/SSE server on %s", addr)
		if err := sseServer.Start(addr); err != nil {
			fmt.Fprintf(os.Stderr, "server error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown transport: %s (use 'stdio' or 'http')\n", *transport)
		os.Exit(1)
	}
}

func envBool(key string) bool {
	v := os.Getenv(key)
	if v == "" {
		return false
	}
	parsed, err := strconv.ParseBool(v)
	return err == nil && parsed
}

func parseTools(raw string) (map[string]bool, error) {
	var names []string
	for _, g := range strings.Split(raw, ",") {
		g = strings.TrimSpace(g)
		if g != "" {
			names = append(names, g)
		}
	}
	return mcpserver.ResolveTools(names)
}
