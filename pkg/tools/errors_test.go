package tools_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/openshift-eng/openshift-ci-mcp/pkg/client"
	"github.com/openshift-eng/openshift-ci-mcp/pkg/tools"
)

func decodeError(t *testing.T, res *mcp.CallToolResult) (string, int) {
	t.Helper()
	if !res.IsError {
		t.Fatal("expected an error result")
	}
	var out struct {
		Error      string `json:"error"`
		StatusCode int    `json:"status_code"`
	}
	text := res.Content[0].(mcp.TextContent).Text
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("error result is not valid JSON: %v: %s", err, text)
	}
	return out.Error, out.StatusCode
}

func TestInvalidParam_ValidJSON(t *testing.T) {
	res, _ := tools.InvalidParam("job_name", `required, got "x"`)
	msg, status := decodeError(t, res)
	if msg != `invalid parameter "job_name": required, got "x"` || status != 400 {
		t.Errorf("got %q %d", msg, status)
	}
}

func TestToolError(t *testing.T) {
	res, _ := tools.ToolError(&client.HTTPError{StatusCode: 404, Body: []byte(`{"msg":"not found"}`)})
	msg, status := decodeError(t, res)
	if msg != `HTTP 404: {"msg":"not found"}` || status != 404 {
		t.Errorf("got %q %d", msg, status)
	}

	res, _ = tools.ToolError(errors.New("boom\nline2\x01"))
	msg, status = decodeError(t, res)
	if msg != "boom\nline2\x01" || status != 500 {
		t.Errorf("got %q %d", msg, status)
	}
}
