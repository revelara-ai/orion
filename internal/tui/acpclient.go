package tui

import (
	"context"
	"io"
	"sync"

	"github.com/revelara-ai/orion/internal/acp"
)

// PaneBuffers is the TUI's session/update sink: it routes streamed updates into
// the panes the developer sees (SPEC §3,§4). agent/thought → Conversation, plan →
// Plan, tool calls → Fleet/Proof.
type PaneBuffers struct {
	mu           sync.Mutex
	Conversation []string
	Plan         []string
	Fleet        []string
	Proof        []string
}

// paneFor maps a session/update kind to its destination pane.
func paneFor(kind string) string {
	switch kind {
	case "plan":
		return "plan"
	case "tool_call", "tool_update", "fleet":
		return "fleet"
	case "proof":
		return "proof"
	default: // agent_thought, agent_message, user_message, …
		return "conversation"
	}
}

// Route appends an update to the pane it belongs to.
func (p *PaneBuffers) Route(u acp.Update) {
	p.mu.Lock()
	defer p.mu.Unlock()
	line := u.Text
	switch paneFor(u.Kind) {
	case "plan":
		p.Plan = append(p.Plan, line)
	case "fleet":
		p.Fleet = append(p.Fleet, line)
	case "proof":
		p.Proof = append(p.Proof, line)
	default:
		p.Conversation = append(p.Conversation, line)
	}
}

// Snapshot returns copies of the pane buffers (safe to read from the render loop).
func (p *PaneBuffers) Snapshot() (conversation, plan, fleet, proof []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := func(s []string) []string { out := make([]string, len(s)); copy(out, s); return out }
	return cp(p.Conversation), cp(p.Plan), cp(p.Fleet), cp(p.Proof)
}

// ApprovalGate maps ACP session/request_permission to Orion's approval/escalation
// gate. In the live TUI a human decides; here it is programmable and records
// requests. It DENIES by default — an unhandled permission is never auto-granted.
type ApprovalGate struct {
	mu       sync.Mutex
	Decide   func(acp.PermissionRequest) acp.PermissionResult
	Requests []acp.PermissionRequest
}

// RequestPermission records the request and returns the gate's decision.
func (g *ApprovalGate) RequestPermission(_ context.Context, req acp.PermissionRequest) (acp.PermissionResult, error) {
	g.mu.Lock()
	g.Requests = append(g.Requests, req)
	decide := g.Decide
	g.mu.Unlock()
	if decide != nil {
		return decide(req), nil
	}
	return acp.PermissionResult{Outcome: "denied"}, nil
}

// ACPClient is the TUI's ACP client half: it drives the (spawned) Conductor
// agent, routes session/update into panes, gates permissions, and serves
// worktree-scoped fs/terminal. This is the default Orion experience.
type ACPClient struct {
	client *acp.Client
	Panes  *PaneBuffers
	Gate   *ApprovalGate
}

// NewACPClient builds the TUI ACP client over the agent's stdout (r) / stdin (w),
// with an approval gate and a worktree-scoped fs.
func NewACPClient(r io.Reader, w io.Writer, gate *ApprovalGate, fs acp.SandboxFS) *ACPClient {
	if gate == nil {
		gate = &ApprovalGate{}
	}
	return &ACPClient{
		client: acp.NewClient(r, w, gate, fs),
		Panes:  &PaneBuffers{},
		Gate:   gate,
	}
}

// Run drives the read loop; call in a goroutine.
func (a *ACPClient) Run(ctx context.Context) error { return a.client.Run(ctx) }

// Initialize negotiates with the Conductor agent.
func (a *ACPClient) Initialize(ctx context.Context) error { return a.client.Initialize(ctx) }

// SessionNew starts a session.
func (a *ACPClient) SessionNew(ctx context.Context) (string, error) { return a.client.SessionNew(ctx) }

// Prompt sends the intent (and completeness answers) and renders the streamed
// updates into the panes.
func (a *ACPClient) Prompt(ctx context.Context, sessionID, text string) (acp.PromptResult, error) {
	return a.client.Prompt(ctx, sessionID, text, a.Panes.Route)
}

// PromptWithUpdates is Prompt plus a caller sink that receives each streamed
// update (with its kind) — used by the chat renderer to render spec cards,
// questions, and plans distinctly. The sink runs in the read loop; because Prompt
// is synchronous, all updates are delivered before it returns (happens-before via
// the response), so a caller may safely collect them and read after Prompt.
func (a *ACPClient) PromptWithUpdates(ctx context.Context, sessionID, text string, onUpdate func(acp.Update)) (acp.PromptResult, error) {
	return a.client.Prompt(ctx, sessionID, text, func(u acp.Update) {
		a.Panes.Route(u)
		if onUpdate != nil {
			onUpdate(u)
		}
	})
}

// Cancel interrupts the session (the interrupt / Red Button path).
func (a *ACPClient) Cancel(ctx context.Context, sessionID string) error {
	return a.client.Cancel(ctx, sessionID)
}
