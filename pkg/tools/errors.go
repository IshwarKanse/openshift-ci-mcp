package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openshift-eng/openshift-ci-mcp/pkg/client"
)

func ToolError(err error) (*mcp.CallToolResult, error) {
	var httpErr *client.HTTPError
	if errors.As(err, &httpErr) {
		return errorResult(httpErr.Error(), httpErr.StatusCode), nil
	}
	return errorResult(err.Error(), http.StatusInternalServerError), nil
}

func InvalidParam(name, detail string) (*mcp.CallToolResult, error) {
	return errorResult(fmt.Sprintf("invalid parameter %q: %s", name, detail), http.StatusBadRequest), nil
}

// errorResult encodes the error as JSON. Building it with %q is not enough:
// Go escapes such as \x01 are not valid JSON.
func errorResult(msg string, status int) *mcp.CallToolResult {
	data, _ := json.Marshal(struct {
		Error      string `json:"error"`
		StatusCode int    `json:"status_code"`
	}{msg, status})
	return mcp.NewToolResultError(string(data))
}
