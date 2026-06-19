package tui

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/acp"
)

// wireAgent attaches a fake Conductor agent (acp.Agent) to the agent end of a
// pipe with the given prompt behavior, and returns the client end.
func wireAgent(t *testing.T, ctx context.Context, prompt acp.PromptFunc) net.Conn {
	t.Helper()
	clientEnd, agentEnd := net.Pipe()
	t.Cleanup(func() { clientEnd.Close(); agentEnd.Close() })
	ag := acp.NewAgent(agentEnd, agentEnd, prompt)
	go ag.Run(ctx)
	return clientEnd
}

// TestACPClientPromptTurn: a prompt turn streams updates that the TUI renders into
// the correct panes (thought → Conversation, plan → Plan, tool call → Fleet).
func TestACPClientPromptTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	prompt := func(_ context.Context, sid, text string, stream func(acp.Update), _ acp.AskFunc) (acp.PromptResult, error) {
		stream(acp.Update{Kind: "agent_thought", Text: "thinking about " + text})
		stream(acp.Update{Kind: "plan", Text: "decompose → prove"})
		stream(acp.Update{Kind: "tool_call", Text: "generator running"})
		return acp.PromptResult{StopReason: "end_turn"}, nil
	}
	conn := wireAgent(t, ctx, prompt)
	c := NewACPClient(conn, conn, nil, nil)
	go c.Run(ctx)

	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	sid, err := c.SessionNew(ctx)
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	if _, err := c.Prompt(ctx, sid, "a service"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	conv, plan, fleet, _ := c.Panes.Snapshot()
	if len(conv) == 0 || !strings.Contains(conv[0], "thinking about a service") {
		t.Fatalf("thought not rendered to Conversation: %v", conv)
	}
	if len(plan) == 0 || !strings.Contains(plan[0], "decompose") {
		t.Fatalf("plan not rendered to Plan pane: %v", plan)
	}
	if len(fleet) == 0 || !strings.Contains(fleet[0], "generator") {
		t.Fatalf("tool call not rendered to Fleet pane: %v", fleet)
	}
}

// TestRequestPermissionMapsToApprovalGate: an agent's request_permission is routed
// to the TUI approval gate and the decision returns to the agent.
func TestRequestPermissionMapsToApprovalGate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got := make(chan acp.PermissionResult, 1)
	clientEnd, agentEnd := net.Pipe()
	defer clientEnd.Close()
	defer agentEnd.Close()

	gate := &ApprovalGate{Decide: func(acp.PermissionRequest) acp.PermissionResult {
		return acp.PermissionResult{Outcome: "granted"}
	}}
	c := NewACPClient(clientEnd, clientEnd, gate, nil)
	go c.Run(ctx)

	// Raw agent peer that asks for permission and captures the client's response.
	agent := acp.NewConn(agentEnd, agentEnd, nil, nil)
	go agent.Run(ctx)
	go func() {
		var res acp.PermissionResult
		_ = agent.Call(ctx, "session/request_permission", acp.PermissionRequest{Title: "delete prod", Kind: "destructive"}, &res)
		got <- res
	}()

	select {
	case r := <-got:
		if r.Outcome != "granted" {
			t.Fatalf("agent got %q, want granted", r.Outcome)
		}
	case <-ctx.Done():
		t.Fatal("permission request not answered")
	}
	if len(gate.Requests) != 1 || gate.Requests[0].Kind != "destructive" {
		t.Fatalf("approval gate did not record the request: %+v", gate.Requests)
	}
}

// TestFsTerminalAccessIsSandboxScoped: the TUI serves fs reads scoped to the
// worktree; reads outside it (the held-out corpus) are rejected.
func TestFsTerminalAccessIsSandboxScoped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	root := t.TempDir()
	corpus := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "ok.txt"), []byte("in"), 0o644)
	_ = os.WriteFile(filepath.Join(corpus, "secret.txt"), []byte("corpus"), 0o644)

	clientEnd, agentEnd := net.Pipe()
	defer clientEnd.Close()
	defer agentEnd.Close()
	c := NewACPClient(clientEnd, clientEnd, nil, acp.ScopedFS{Root: root})
	go c.Run(ctx)

	agent := acp.NewConn(agentEnd, agentEnd, nil, nil)
	go agent.Run(ctx)

	var rd struct {
		Content string `json:"content"`
	}
	if err := agent.Call(ctx, "fs/read_text_file", map[string]string{"path": "ok.txt"}, &rd); err != nil || rd.Content != "in" {
		t.Fatalf("in-scope read failed: content=%q err=%v", rd.Content, err)
	}
	if err := agent.Call(ctx, "fs/read_text_file", map[string]string{"path": filepath.Join(corpus, "secret.txt")}, &rd); err == nil {
		t.Fatal("out-of-scope corpus read must be rejected")
	}
}
