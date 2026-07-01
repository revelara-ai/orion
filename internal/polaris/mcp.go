package polaris

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// mcpProtocolVersion is the MCP revision Orion negotiates. Sent on initialize and as the
// MCP-Protocol-Version header on every subsequent request (per the Streamable HTTP transport).
const mcpProtocolVersion = "2025-06-18"

// MCPClient is a minimal Streamable-HTTP MCP client (JSON-RPC 2.0) for the Polaris (revelara.ai)
// MCP service. It carries a Bearer token (obtained out-of-band by the WorkOS OAuth flow) and echoes
// the server-issued session id. Every call is time-bounded (Harness Reliability: the MCP service is
// an external dependency). Hand-rolled + dependency-free (CGO_ENABLED=0), matching internal/acp.
type MCPClient struct {
	endpoint  string
	token     string
	http      *http.Client
	sessionID string
	nextID    int
}

// NewMCPClient returns a client for a Polaris MCP endpoint with a bounded per-call timeout.
func NewMCPClient(endpoint, token string) *MCPClient {
	return &MCPClient{
		endpoint: endpoint,
		token:    token,
		http:     &http.Client{Timeout: 15 * time.Second},
	}
}

// mcpEnvelope is a JSON-RPC 2.0 message. A request has ID+Method; a notification has Method and no
// ID; a response has ID and Result|Error.
type mcpEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ServerInfo identifies the MCP server (from the initialize handshake).
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Tool is an MCP tool descriptor (from tools/list).
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// ToolContent is one content block of a tool result.
type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ToolResult is a tools/call result.
type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError"`
}

// Initialize performs the MCP handshake, captures the session id, and signals readiness. It returns
// the server identity so callers can log/verify what they connected to.
func (c *MCPClient) Initialize(ctx context.Context) (ServerInfo, error) {
	params := map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "orion", "version": "v2"},
	}
	raw, err := c.rpc(ctx, "initialize", params)
	if err != nil {
		return ServerInfo{}, err
	}
	var out struct {
		ServerInfo ServerInfo `json:"serverInfo"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return ServerInfo{}, fmt.Errorf("mcp initialize: decode: %w", err)
	}
	// notifications/initialized is a JSON-RPC notification — no response expected.
	_ = c.notify(ctx, "notifications/initialized", map[string]any{})
	return out.ServerInfo, nil
}

// ListTools returns the tools the MCP server exposes.
func (c *MCPClient) ListTools(ctx context.Context) ([]Tool, error) {
	raw, err := c.rpc(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("mcp tools/list: decode: %w", err)
	}
	return out.Tools, nil
}

// CallTool invokes a tool by name with arguments and returns its structured result.
func (c *MCPClient) CallTool(ctx context.Context, name string, args any) (ToolResult, error) {
	raw, err := c.rpc(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return ToolResult{}, err
	}
	var res ToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return ToolResult{}, fmt.Errorf("mcp tools/call %s: decode: %w", name, err)
	}
	return res, nil
}

// rpc sends a JSON-RPC request and returns the result payload (or an error, including a
// JSON-RPC-level error from the server).
func (c *MCPClient) rpc(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.nextID++
	id, _ := json.Marshal(c.nextID)
	body, _ := json.Marshal(mcpEnvelope{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	resp, err := c.do(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("mcp %s: %w", method, err)
	}
	defer resp.Body.Close()
	c.captureSession(resp)
	raw, err := readRPCResult(resp)
	if err != nil {
		return nil, fmt.Errorf("mcp %s: %w", method, err)
	}
	return raw, nil
}

// notify sends a JSON-RPC notification (no id) and ignores the (empty) response.
func (c *MCPClient) notify(ctx context.Context, method string, params any) error {
	body, _ := json.Marshal(mcpEnvelope{JSONRPC: "2.0", Method: method, Params: params})
	resp, err := c.do(ctx, body)
	if err != nil {
		return err
	}
	c.captureSession(resp)
	_ = resp.Body.Close()
	return nil
}

func (c *MCPClient) do(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	return c.http.Do(req)
}

func (c *MCPClient) captureSession(resp *http.Response) {
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
		c.sessionID = s
	}
}

// readRPCResult parses a JSON-RPC response from either application/json or a single-message
// text/event-stream (the two Streamable-HTTP response shapes). A 202 (notification ack) yields no
// result.
func readRPCResult(resp *http.Response) (json.RawMessage, error) {
	if resp.StatusCode == http.StatusAccepted {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		payload = extractSSEData(payload)
	}
	var env mcpEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if env.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", env.Error.Code, env.Error.Message)
	}
	return env.Result, nil
}

// extractSSEData concatenates the `data:` lines of an SSE stream into the JSON payload.
func extractSSEData(b []byte) []byte {
	var data []string
	for _, line := range strings.Split(string(b), "\n") {
		if s, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimSpace(s))
		}
	}
	if len(data) == 0 {
		return b
	}
	return []byte(strings.Join(data, ""))
}
