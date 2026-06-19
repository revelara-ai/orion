package conductor

import (
	"context"
	"io"

	"github.com/revelara-ai/orion/internal/acp"
)

// ConductorAgent is the Conductor exposed as an ACP agent (SPEC §3): primed by a
// role template, it runs the prompt-turn conversation, streams plan/thought
// updates, and delegates the hard decisions to the deterministic gates. In
// production the reasoning is the spawned vendor agent; this Go agent is the
// reference/headless Conductor and the seam under test.
type ConductorAgent struct {
	Role RoleTemplate
}

// Prompt runs one turn: it acknowledges intent and surfaces a plan via
// session/update, then ends the turn. Proof, the deployment bar, and leases are
// NOT decided here — they are deterministic tools the Conductor invokes elsewhere
// (the gate), never overrides.
func (ca ConductorAgent) Prompt(ctx context.Context, sessionID, text string, stream func(acp.Update)) (acp.PromptResult, error) {
	stream(acp.Update{Kind: "agent_thought", Text: "intake: " + text})
	stream(acp.Update{Kind: "plan", Text: "decompose → generate → prove (deterministic) → deliver"})
	if ctx.Err() != nil {
		return acp.PromptResult{StopReason: "cancelled"}, nil
	}
	return acp.PromptResult{StopReason: "end_turn"}, nil
}

// Serve runs the Conductor as an ACP agent over the given transport.
func (ca ConductorAgent) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	return acp.NewAgent(r, w, ca.Prompt).Run(ctx)
}
