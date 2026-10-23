package domain

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/openshift-eng/openshift-ci-mcp/pkg/client"
	"github.com/openshift-eng/openshift-ci-mcp/pkg/tools"
)

func RegisterSearchTools(s *server.MCPServer, search client.SearchCI) {
	s.AddTool(mcp.NewTool("search_ci_logs",
		mcp.WithDescription("Search logs and JUnit output across OpenShift CI for error messages, test names, or patterns."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search string (error message, test name, or pattern)")),
		mcp.WithString("max_age", mcp.Description("Max age (e.g. '7d', '24h')")),
		mcp.WithString("type", mcp.Description("Type: 'all', 'bug', 'junit' (default: 'all')")),
		mcp.WithNumber("limit", mcp.Description("Max results to return (default 25)"), mcp.DefaultNumber(25)),
		mcp.WithString("fields", mcp.Description("Comma-separated list of field names to include in response (default: all)")),
	), SearchCILogsHandler(search))
}

func SearchCILogsHandler(search client.SearchCI) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, err := req.RequireString("query")
		if err != nil {
			return tools.InvalidParam("query", "required")
		}
		params := map[string]string{
			"maxResults": fmt.Sprintf("%d", req.GetInt("limit", 25)),
		}
		if maxAge := req.GetString("max_age", ""); maxAge != "" {
			normalized, err := normalizeMaxAge(maxAge)
			if err != nil {
				return tools.InvalidParam("max_age", err.Error())
			}
			params["maxAge"] = normalized
		}
		if searchType := req.GetString("type", ""); searchType != "" {
			params["type"] = searchType
		}
		data, err := search.Search(ctx, query, params)
		if err != nil {
			return tools.ToolError(err)
		}
		if trimmed, err := client.ReshapeJSON[client.SearchResponse](data); err == nil {
			data = trimmed
		}
		if filtered, err := client.FilterFields(data, req.GetString("fields", "")); err == nil {
			data = filtered
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

var daysPattern = regexp.MustCompile(`^(\d+)d$`)

// maxAgeDays bounds day values so the converted hours stay a valid Go
// duration (about 292 years).
const maxAgeDays = math.MaxInt64 / int64(24*time.Hour)

// normalizeMaxAge converts a day count such as "7d" to hours ("168h"),
// since Search.CI only accepts Go durations. Other values pass through.
func normalizeMaxAge(maxAge string) (string, error) {
	m := daysPattern.FindStringSubmatch(maxAge)
	if m == nil {
		return maxAge, nil
	}
	days, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || days > maxAgeDays {
		return "", fmt.Errorf("day count too large")
	}
	return fmt.Sprintf("%dh", days*24), nil
}
