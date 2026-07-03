package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Update is a session/update notification streamed by the agent during a prompt
// turn (agent/thought chunks, plans, tool calls — SPEC §4).
type Update struct {
	SessionID string          `json:"sessionId"`
	Kind      string          `json:"kind"`
	Text      string          `json:"text,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

// PromptResult is the terminal result of a prompt turn.
type PromptResult struct {
	StopReason string `json:"stopReason"`
}

// PermissionRequest is the agent's session/request_permission payload.
type PermissionRequest struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
	Kind      string `json:"kind"` // spec_ratify | tool | hitl …
	// Tool fields (Kind == "tool"): the pending mutating tool and a preview to render in
	// the approval card (a bash command, or a file path + unified-diff/content preview).
	Tool    string `json:"tool,omitempty"`
	Preview string `json:"preview,omitempty"`
}

// PermissionResult is the gate's decision. For a spec ratify: "granted" | "denied". For a
// tool approval: "allow_once" | "allow_always" | "deny".
type PermissionResult struct {
	Outcome string `json:"outcome"`
}

// PermissionGate is Orion's approval/escalation gate (SPEC §3): a human (or
// policy) authorizes through the TUI. A permission grant is NOT proof.
type PermissionGate interface {
	RequestPermission(ctx context.Context, req PermissionRequest) (PermissionResult, error)
}

// SandboxFS serves the agent's fs/* and terminal/* requests under Orion's safety
// controls. Implementations MUST reject paths outside their scope.
type SandboxFS interface {
	ReadTextFile(path string) (string, error)
	WriteTextFile(path, content string) error
	Terminal(command string, args []string) (string, error)
}

// Client is the ACP client: it drives a spawned agent and serves the
// agent-exposed client methods under Orion's gates.
type Client struct {
	conn *Conn
	gate PermissionGate
	fs   SandboxFS

	mu    sync.Mutex
	onUpd func(Update) // active prompt's update sink
}

// NewClient builds a client over the agent's stdout (r) and stdin (w).
func NewClient(r io.Reader, w io.Writer, gate PermissionGate, fs SandboxFS) *Client {
	c := &Client{gate: gate, fs: fs}
	c.conn = NewConn(r, w, c.handle, c.onNotify)
	return c
}

// Run drives the read loop; call in a goroutine.
func (c *Client) Run(ctx context.Context) error { return c.conn.Run(ctx) }

// handle answers agent→client requests under Orion's safety gates.
func (c *Client) handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "session/request_permission":
		if c.gate == nil {
			return nil, fmt.Errorf("no permission gate configured")
		}
		var req PermissionRequest
		_ = json.Unmarshal(params, &req)
		return c.gate.RequestPermission(ctx, req)

	case "fs/read_text_file":
		var p struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(params, &p)
		if c.fs == nil {
			return nil, fmt.Errorf("no sandbox fs")
		}
		content, err := c.fs.ReadTextFile(p.Path)
		if err != nil {
			return nil, err
		}
		return map[string]string{"content": content}, nil

	case "fs/write_text_file":
		var p struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		_ = json.Unmarshal(params, &p)
		if c.fs == nil {
			return nil, fmt.Errorf("no sandbox fs")
		}
		if err := c.fs.WriteTextFile(p.Path, p.Content); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil

	case "terminal/create", "terminal/output":
		var p struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		}
		_ = json.Unmarshal(params, &p)
		if c.fs == nil {
			return nil, fmt.Errorf("no sandbox fs")
		}
		out, err := c.fs.Terminal(p.Command, p.Args)
		if err != nil {
			return nil, err
		}
		return map[string]string{"output": out}, nil
	}
	return nil, fmt.Errorf("method not found: %s", method)
}

func (c *Client) onNotify(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	c.mu.Lock()
	sink := c.onUpd
	c.mu.Unlock()
	if sink == nil {
		return
	}
	var u Update
	_ = json.Unmarshal(params, &u)
	u.Raw = params
	sink(u)
}

// Initialize negotiates protocol/capabilities with the agent.
func (c *Client) Initialize(ctx context.Context) error {
	return c.conn.Call(ctx, "initialize", map[string]any{"protocolVersion": 1}, nil)
}

// SessionNew starts a new session and returns its id.
func (c *Client) SessionNew(ctx context.Context) (string, error) {
	var res struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.conn.Call(ctx, "session/new", map[string]any{}, &res); err != nil {
		return "", err
	}
	return res.SessionID, nil
}

// Prompt sends an intent and streams session/update notifications to onUpdate
// until the turn completes (the session/prompt response).
func (c *Client) Prompt(ctx context.Context, sessionID, text string, onUpdate func(Update)) (PromptResult, error) {
	c.mu.Lock()
	c.onUpd = onUpdate
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.onUpd = nil
		c.mu.Unlock()
	}()

	var res PromptResult
	err := c.conn.Call(ctx, "session/prompt", map[string]any{"sessionId": sessionID, "text": text}, &res)
	return res, err
}

// Cancel requests cancellation of the session (interrupt / Red Button path).
func (c *Client) Cancel(ctx context.Context, sessionID string) error {
	return c.conn.Notify("session/cancel", map[string]any{"sessionId": sessionID})
}

// Control runs an out-of-turn session control op (compact | model) and returns the
// agent's human-readable result.
func (c *Client) Control(ctx context.Context, sessionID, op, arg string) (string, error) {
	var res struct {
		Result string `json:"result"`
	}
	err := c.conn.Call(ctx, "session/control", map[string]any{"sessionId": sessionID, "op": op, "arg": arg}, &res)
	return res.Result, err
}

// ScopedFS is a SandboxFS rooted at a single directory (the task worktree). Any
// path that resolves outside Root — including the held-out proof corpus — is
// rejected: ACP file/terminal access is not a sandbox bypass (SPEC §3).
type ScopedFS struct {
	Root      string
	AllowExec bool
}

func (s ScopedFS) resolve(path string) (string, error) {
	root, err := filepath.Abs(s.Root)
	if err != nil {
		return "", err
	}
	p := path
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	p = filepath.Clean(p)
	if p != root && !strings.HasPrefix(p, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("acp: path %q is outside the task scope", path)
	}
	return p, nil
}

// ReadTextFile reads a file inside the scope.
func (s ScopedFS) ReadTextFile(path string) (string, error) {
	p, err := s.resolve(path)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	return string(b), err
}

// WriteTextFile writes a file inside the scope.
func (s ScopedFS) WriteTextFile(path, content string) error {
	p, err := s.resolve(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

// Terminal is intentionally minimal in V2.0: execution flows through the sandbox
// + dry-run gates elsewhere; here it is disabled unless explicitly allowed.
func (s ScopedFS) Terminal(command string, args []string) (string, error) {
	if !s.AllowExec {
		return "", fmt.Errorf("acp: terminal execution not permitted in this scope")
	}
	return "", fmt.Errorf("acp: terminal execution must route through the sandbox gate")
}
