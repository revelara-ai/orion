package polaris

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockMCP is an httptest server speaking the MCP subset the client uses. It records the auth +
// session headers it saw so the test can assert the client presents them.
func mockMCP(t *testing.T) (url string, seen *[]http.Header) {
	t.Helper()
	var hdrs []http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdrs = append(hdrs, r.Header.Clone())
		var req mcpEnvelope
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Mcp-Session-Id", "sess-123")

		// A notification (no id) gets a bare 202 with no body.
		if len(req.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		reply := func(result any) {
			b, _ := json.Marshal(result)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(mcpEnvelope{JSONRPC: "2.0", ID: req.ID, Result: b})
		}
		switch req.Method {
		case "initialize":
			reply(map[string]any{"protocolVersion": mcpProtocolVersion, "serverInfo": ServerInfo{Name: "revelara", Version: "1.0"}})
		case "tools/list":
			reply(map[string]any{"tools": []Tool{{Name: "search_controls", Description: "search reliability controls"}}})
		case "tools/call":
			reply(ToolResult{Content: []ToolContent{{Type: "text", Text: `[{"code":"RC-012"}]`}}})
		default:
			http.Error(w, "unknown method: "+req.Method, http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &hdrs
}

// TestMCPClientRoundTrip (or-xe7.1): the client completes the initialize handshake (capturing the
// session id), lists tools, and calls a tool — presenting the Bearer token and echoing the session
// id on every request.
func TestMCPClientRoundTrip(t *testing.T) {
	url, seen := mockMCP(t)
	c := NewMCPClient(url, "tok-abc")
	ctx := context.Background()

	info, err := c.Initialize(ctx)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if info.Name != "revelara" {
		t.Errorf("serverInfo name = %q, want revelara", info.Name)
	}
	if c.sessionID != "sess-123" {
		t.Errorf("session id not captured from initialize: %q", c.sessionID)
	}

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "search_controls" {
		t.Errorf("tools = %+v, want [search_controls]", tools)
	}

	res, err := c.CallTool(ctx, "search_controls", map[string]any{"query": "cache stampede"})
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	if res.IsError {
		t.Error("tool reported IsError unexpectedly")
	}
	if len(res.Content) == 0 || res.Content[0].Text == "" {
		t.Errorf("empty tool result: %+v", res)
	}

	// Every request carried the token; every request AFTER initialize echoed the session id.
	if len(*seen) < 4 {
		t.Fatalf("expected >=4 requests (initialize, initialized, list, call), got %d", len(*seen))
	}
	for i, h := range *seen {
		if got := h.Get("Authorization"); got != "Bearer tok-abc" {
			t.Errorf("request %d Authorization = %q, want Bearer tok-abc", i, got)
		}
		if h.Get("MCP-Protocol-Version") != mcpProtocolVersion {
			t.Errorf("request %d missing MCP-Protocol-Version", i)
		}
		if i > 0 && h.Get("Mcp-Session-Id") != "sess-123" {
			t.Errorf("request %d did not echo the session id", i)
		}
	}
}

// TestMCPClientSurfacesRPCError: a JSON-RPC error from the server becomes a Go error.
func TestMCPClientSurfacesRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcpEnvelope
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mcpEnvelope{JSONRPC: "2.0", ID: req.ID, Error: &mcpError{Code: -32601, Message: "method not found"}})
	}))
	t.Cleanup(srv.Close)

	if _, err := NewMCPClient(srv.URL, "t").ListTools(context.Background()); err == nil {
		t.Fatal("a JSON-RPC error must surface as a Go error")
	}
}
