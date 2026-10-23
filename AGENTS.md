# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commit Messages

Use semantic commit prefixes: `feat:`, `fix:`, `docs:`, `chore:`, `test:`, `refactor:`. Keep the subject line concise and lowercase after the prefix.

## Build & Test Commands

```bash
make build              # Build binary to bin/
make test               # Run unit tests
make test-integration   # Run integration tests (requires network)
make lint               # Run go vet
make smoke              # Build + smoke test against binary
make check              # Build + run mcpchecker eval suite
make image              # Build container image
make push               # Push to container registry
make generate           # Regenerate tool tables in README
make clean              # Remove build artifacts
make verify             # Run all verification targets
```

## Architecture

MCP server exposing OpenShift CI data (Sippy, Release Controller, Search.CI, Prow) as 26 tools. All read-only, no authentication.

**Layers (top to bottom):**

1. **Transport** (`cmd/openshift-ci-mcp/main.go`) — CLI flag parsing, stdio or HTTP/SSE via mcp-go
2. **Server** (`pkg/server/server.go`) — Creates all clients, registers all tools. This is the wiring hub.
3. **Tools** — Two tiers:
   - `pkg/tools/domain/` — Intent-oriented tools with parameter defaults, variant filtering, response composition. Each file groups related tools (jobs.go, tests.go, component.go, etc.)
   - `pkg/tools/proxy/` — Raw passthrough to upstream APIs, one file per data source
4. **Clients** (`pkg/client/`) — Thin HTTP wrappers. Interfaces: `Sippy`, `ReleaseController`, `SearchCI`, which return `([]byte, error)`, and `Prow`, which parses deck job history and gcsweb listings and streams objects.

**Key patterns:**

- **Handler closures**: Every tool handler is a function that takes a client interface and returns `server.ToolHandlerFunc`. The client is captured in the closure. Example: `GetJobReportHandler(sippy client.Sippy) server.ToolHandlerFunc`.
- **Variant filtering**: Domain tools that query jobs/tests call `extractVariantParams(req)` (defined in `jobs.go`) to pull arch/topology/platform/network from the MCP request, then `filter.MergeInto()` to encode them as Sippy filter JSON. Additional non-variant filters use `filter.MergeItemInto()`.
- **Release resolution**: When `release` param is omitted, `tools.ResolveRelease()` fetches `/api/releases` and picks the first release without a GA date.
- **Error handling**: `tools.ToolError(err)` wraps errors as MCP tool results with `isError: true`. HTTP errors from clients are `*client.HTTPError` with status code and body.

**Testing**: Domain tool tests use `mockSippy` and `capturingSippy` from `testhelpers_test.go`. Mock provides canned responses by path. CapturingSippy lets tests inspect what parameters were sent. Client tests use `httptest.NewServer`.

## Adding a New Tool

1. Add handler function and registration in the appropriate `pkg/tools/domain/*.go` file
2. If it needs variant filtering, call `extractVariantParams(req)` and `filter.MergeInto()`
3. Register it in `pkg/server/server.go` inside `New()`
4. Add tests using `newMockSippy()` in a `*_test.go` file in the same package

## Data Sources

- **Sippy** (`sippy.dptools.openshift.org`) — Central CI analysis. Most tools hit this.
- **Release Controller** (`{arch}.ocp.releases.ci.openshift.org`) — Payload tags and acceptance status. Used by `get_payload_status` and `release_controller_api`.
- **Search.CI** (`search.ci.openshift.org`) — Log and JUnit search. Used by `search_ci_logs` and `search_ci_api`.
- **Prow** (`prow.ci.openshift.org` job history, gcsweb for the `test-platform-results-public` bucket) — Run history, run results and steps, logs and artifacts. Used by the `get_prow_*` tools. Decks are listed in `client.DefaultDecks`.
