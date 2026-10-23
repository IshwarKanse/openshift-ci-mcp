# openshift-ci-mcp

![Go](https://img.shields.io/github/go-mod/go-version/openshift-eng/openshift-ci-mcp)
![License](https://img.shields.io/github/license/openshift-eng/openshift-ci-mcp)
![Release](https://img.shields.io/github/v/release/openshift-eng/openshift-ci-mcp)

MCP server providing read-only access to OpenShift CI data. Query Sippy, Release Controller, Search.CI, and Prow job logs and artifacts from any MCP-compatible client.

## Tools

<!-- Tool tables are auto-generated from source. Run `make generate` to update. -->

### Domain Tools

<!-- BEGIN DOMAIN TOOLS -->
| Tool | Group | Description |
| ---- | ----- | ----------- |
| `get_release_health` | `core` | Use to get health data for a specific release such as success rates, variant summary, and payload acceptance. |
| `get_releases` | `core` | Use to get OpenShift releases with availability and dev cycle dates |
| `get_variants` | `core` | Use to list variants and their possible values (arch, topology, platform, network, etc.). |
| `get_component_readiness` | `payload` | Use to get a report on component readiness for the current dev cycle. Can be slow (30+ seconds). Passing a view name avoids an extra API call to discover views. |
| `get_payload_diff` | `payload` | Use to list pull request changes between payload tags. |
| `get_payload_status` | `payload` | Use to get recent payload acceptance status from the Release Controller. |
| `get_payload_test_failures` | `payload` | Use to get test failures for payload job runs |
| `get_regression_detail` | `payload` | Use when you need details about a regression with triages and Jiras. |
| `get_regressions` | `payload` | Use to get tests performing significantly worse than the previous release |
| `get_job_report` | `jobs` | Use to get CI job pass rates with filtering and pagination |
| `get_job_run_summary` | `jobs` | Use to get test failures and cluster operator status of a single job run |
| `get_job_runs` | `jobs` | Use to get results, timings, and risk analysis of recent job runs |
| `get_ci_test_report` | `tests` | Use to get pass/fail/flake rates for tests with optional filtering |
| `get_recent_test_failures` | `tests` | Tests that recently started failing, useful for detecting new regressions. |
| `get_test_details` | `tests` | Use to get pass rates broken down by variant and by job. |
| `get_pr_impact` | `prs` | Use to get test failure impact data for a specific, known pull request. Rate-limited to 20 req/hour. |
| `get_release_prs` | `prs` | Use to get a list of pull requests for a specific release or presubmits |
| `search_ci_logs` | `search` | Search logs and JUnit output across OpenShift CI for error messages, test names, or patterns. |
| `get_prow_artifact_content` | `prow` | Use to read a Prow job run's build log, a step's build log, or any text artifact, with tail, head or grep to keep the output small. Defaults to the last 200 lines of the run's build-log.txt. |
| `get_prow_artifacts` | `prow` | Use to list the files and directories of a Prow job run, such as artifacts/<test>/<step>/ |
| `get_prow_job_history` | `prow` | Use to list all runs of a Prow job, including pending and aborted runs, presubmits and /pj-rehearse runs that Sippy does not track |
| `get_prow_job_run` | `prow` | Use to get one Prow job run's result, timing, refs and the pass/fail of each step, with failure excerpts and the test directories that hold step logs |
<!-- END DOMAIN TOOLS -->

### Proxy Tools

Raw passthrough to upstream APIs for advanced use cases. Disabled by default — enable with `--enable-proxy-tools` or `ENABLE_PROXY_TOOLS=true`.

<!-- BEGIN PROXY TOOLS -->
| Tool | Group | Description |
| ---- | ----- | ----------- |
| `release_controller_api` | `proxies` | Raw passthrough to the Release Controller API. |
| `search_ci_api` | `proxies` | Raw passthrough to the Search.CI API. |
| `sippy_api` | `proxies` | Raw passthrough to any Sippy API endpoint. |
<!-- END PROXY TOOLS -->

## Usage

### stdio (default) (recommended)

```bash
# Run directly
bin/openshift-ci-mcp

# Run in container
podman run -i --rm quay.io/rh-edge-enablement/openshift-ci-mcp
```

### HTTP/SSE

```bash
bin/openshift-ci-mcp --transport http --port 8080

# Or in container
podman run -p 8080:8080 quay.io/rh-edge-enablement/openshift-ci-mcp --transport http --port 8080
```

### Claude Desktop

Local binary:

```json
{
  "mcpServers": {
    "openshift-ci": {
      "command": "/path/to/openshift-ci-mcp"
    }
  }
}
```

Container:

```json
{
  "mcpServers": {
    "openshift-ci": {
      "command": "podman",
      "args": ["run", "-i", "--rm", "quay.io/rh-edge-enablement/openshift-ci-mcp"]
    }
  }
}
```

`go run` (convenient when podman is not available, e.g. running Claude in a pod):

```json
{
  "mcpServers": {
    "openshift-ci": {
      "command": "go",
      "args": [
        "run",
        "github.com/openshift-eng/openshift-ci-mcp/cmd/openshift-ci-mcp@latest"
      ]
    }
  }
}
```

### Claude Code

```bash
claude mcp add openshift-ci go -- run github.com/openshift-eng/openshift-ci-mcp/cmd/openshift-ci-mcp@latest
```

## Configuration

### CLI Flags

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `--transport` | `stdio` | Transport mode: `stdio` or `http` |
| `--port` | `8080` | HTTP port (only used with `--transport http`) |
| `--timeout` | `30s` | Upstream request timeout |
| `--tools` | all domain groups | Comma-separated tool groups to enable (see [Tool Groups](#tool-groups)) |
| `--enable-proxy-tools` | `false` | Add proxy tools on top of the active tool groups |

### Environment Variables

| Variable | Default | Description |
| -------- | ------- | ----------- |
| `SIPPY_URL` | `https://sippy.dptools.openshift.org` | Sippy base URL |
| `RELEASE_CONTROLLER_URL` | `https://amd64.ocp.releases.ci.openshift.org` | Release Controller base URL |
| `SEARCH_CI_URL` | `https://search.ci.openshift.org` | Search.CI base URL |
| `MCP_TOOLS` | all domain groups | Comma-separated tool groups to enable (see [Tool Groups](#tool-groups)) |
| `ENABLE_PROXY_TOOLS` | `false` | Set to `true` to add proxy tools on top of the active tool groups |
| `OPENSHIFT_CI_TOKEN` | unset | OpenShift OAuth token for the CI cluster, needed for the `qe-private` deck (see [Private Prow Decks](#private-prow-decks)) |
| `PROW_DECKS` | unset | JSON list of extra Prow decks, or replacements for built-in ones by name (see [Private Prow Decks](#private-prow-decks)) |

### Private Prow Decks

The `prow` tools know two decks: `public` (`prow.ci.openshift.org`, bucket `test-platform-results-public`) and `qe-private` (bucket `qe-private-deck`), which needs a token. Get one by logging in to the CI cluster and pass it in `OPENSHIFT_CI_TOKEN`:

```bash
oc login --web https://api.ci.l2s4.p1.openshiftapps.com:6443
export OPENSHIFT_CI_TOKEN="$(oc whoami -t)"
```

A ServiceAccount token does not work. The token expires, so refresh the variable and restart the server when the tools report it rejected. It is sent only to the deck and gcsweb hosts of a deck that needs it, never in tool output, and not across redirects to other hosts.

Other decks can be added with `PROW_DECKS`, for example:

```bash
export PROW_DECKS='[{"name":"my-deck","deck_url":"https://deck.example.com","gcsweb_url":"https://gcsweb.example.com","buckets":["my-bucket"],"token_env":"MY_DECK_TOKEN"}]'
```

> **HTTP/SSE mode:** a token in the server's environment lets every client that can reach the server read that private deck. Only set it for a server that you alone use, such as the default stdio mode.

### Tool Groups

By default all domain tools are enabled. Use `--tools` to selectively enable only the groups you need:

```bash
# Enable only CI job and test tools
bin/openshift-ci-mcp --tools jobs,tests

# Enable everything including proxy tools
bin/openshift-ci-mcp --tools core,payload,jobs,tests,prs,search,prow,proxies
```

<!-- BEGIN TOOL GROUPS -->
| Group | Description | Tools |
| ----- | ----------- | ----- |
| `core` | Release metadata and variant dimensions | `get_releases`, `get_release_health`, `get_variants`, `get_tool_fields` |
| `payload` | Component readiness, regressions, and payload acceptance | `get_component_readiness`, `get_regressions`, `get_regression_detail`, `get_payload_status`, `get_payload_diff`, `get_payload_test_failures` |
| `jobs` | CI job pass rates and run history | `get_job_report`, `get_job_runs`, `get_job_run_summary` |
| `tests` | Test pass/fail/flake rates and recent failures | `get_ci_test_report`, `get_test_details`, `get_recent_test_failures` |
| `prs` | Pull request listings and CI impact | `get_release_prs`, `get_pr_impact` |
| `search` | Build log and JUnit failure search | `search_ci_logs` |
| `prow` | Prow job history, run steps, logs and artifacts | `get_prow_job_history`, `get_prow_job_run`, `get_prow_artifacts`, `get_prow_artifact_content` |
| `proxies` | Raw passthrough to upstream APIs | `sippy_api`, `release_controller_api`, `search_ci_api` |
<!-- END TOOL GROUPS -->

## Variant Filtering

Tools that query jobs or tests accept variant parameters for filtering:

- `arch` — amd64, arm64, ppc64le, s390x, multi
- `topology` — ha, single, compact, external, microshift
- `platform` — aws, azure, gcp, metal, vsphere, rosa, etc.
- `network` — ovn, sdn, cilium
- `variants` — map of any other dimension (e.g. `{"Installer": "upi", "SecurityMode": "fips"}`)

Use `get_variants` to discover all available dimensions and values.

## Build

```bash
make build              # Build binary to bin/
make test               # Run unit tests
make test-integration   # Run integration tests (requires network)
make lint               # Run go vet
make smoke              # Build + smoke test against binary
make check              # Build + run mcpchecker eval suite
make image              # Build container image
make push               # Push to registry
make generate           # Regenerate tool tables in README
make clean              # Remove build artifacts
```

Requires Go 1.24+.

## Data Sources

All read-only. Only the private Prow deck needs authentication.

| Source | URL | Purpose |
| ------ | --- | ------- |
| Sippy | sippy.dptools.openshift.org | Jobs, tests, component readiness, regressions, payloads, health |
| Release Controller | {arch}.ocp.releases.ci.openshift.org | Payload tags, acceptance status |
| Search.CI | search.ci.openshift.org | Build log and JUnit failure search |
| Prow | prow.ci.openshift.org and its gcsweb; the private QE deck | Job history, run results and steps, logs and artifacts (`test-platform-results-public` and, with a token, `qe-private-deck`) |
