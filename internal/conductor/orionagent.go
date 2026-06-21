package conductor

import (
	"context"
	"io"
	"sync"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/harness"
	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

// OrionAgent is the native, LLM-driven human-interface agent (SPEC §0 amendment:
// "Orion" = the agent the developer talks to). It runs the harness ReAct loop
// over a model Provider, primed by the adversarial-grill RoleTemplate, and turns
// the developer's intent into a ratified spec by CALLING the deterministic spec
// tools (gates-as-tools). It implements acp.PromptFunc, so the existing TUI drives
// it identically to the deterministic fallback — the UI can't tell which brain it
// is talking to.
type OrionAgent struct {
	provider  llm.Provider
	conductor *orchestrator.Conductor
	role      RoleTemplate

	mu       sync.Mutex
	sessions map[string][]llm.Message
}

// NewOrionAgent builds the native agent over the given model provider.
func NewOrionAgent(p llm.Provider, c *orchestrator.Conductor, role RoleTemplate) *OrionAgent {
	return &OrionAgent{provider: p, conductor: c, role: role, sessions: map[string][]llm.Message{}}
}

// Serve runs Orion as an ACP agent over the transport (same shape as the
// deterministic ConductorAgent.Serve, so it's a drop-in backend).
func (a *OrionAgent) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	return acp.NewAgent(r, w, a.Prompt).Run(ctx)
}

// Prompt runs one developer turn through the harness loop: the model reasons,
// grills, and calls the spec tools until it ends the turn. Thoughts stream to the
// Conversation pane; tool calls stream to Fleet; a ratified spec surfaces a plan
// update.
func (a *OrionAgent) Prompt(ctx context.Context, sessionID, text string, stream func(acp.Update), ask acp.AskFunc) (acp.PromptResult, error) {
	end := acp.PromptResult{StopReason: "end_turn"}

	a.mu.Lock()
	convo := append([]llm.Message(nil), a.sessions[sessionID]...)
	a.mu.Unlock()
	convo = append(convo, llm.TextMessage(llm.RoleUser, text))

	loop := harness.Loop{
		Provider:   a.provider,
		Tools:      specTools(a.conductor, a.provider),
		System:     a.systemPrompt(),
		Supervisor: harness.Supervisor{MaxIterations: 16, Budget: a.conductor.Budget()},
	}
	convo, _, err := loop.Run(ctx, convo, func(e harness.Event) {
		switch e.Kind {
		case harness.EventThought:
			// Stream every non-empty delta verbatim — whitespace deltas (spaces,
			// newlines between tokens) must survive or the streamed text runs together.
			if e.Text != "" {
				stream(acp.Update{Kind: "agent_message", Text: e.Text})
			}
		case harness.EventToolCall:
			stream(acp.Update{Kind: "tool_call", Text: "· " + e.Tool})
		case harness.EventToolResult:
			// The build pipeline's phase report renders as a distinct card.
			if e.Tool == "build_service" && !e.Error {
				stream(acp.Update{Kind: "build_report", Text: e.Text})
			}
		}
	})

	a.mu.Lock()
	a.sessions[sessionID] = convo
	a.mu.Unlock()

	if err != nil {
		stream(acp.Update{Kind: "agent_message", Text: "I hit a problem driving this turn: " + err.Error()})
		return end, nil
	}
	// Surface ratification as a plan signal (the TUI renders it distinctly).
	if sv, e := a.conductor.SpecView(ctx); e == nil && sv.Status == "accepted" {
		stream(acp.Update{Kind: "plan", Text: "Spec ratified ✓"})
	}
	return end, nil
}

// systemPrompt primes Orion: the role persona + invariants + how to use the spec
// tools to grill the intent into a ratified spec.
func (a *OrionAgent) systemPrompt() string {
	return a.role.Render() + `

## Your job (the grill → spec loop)
You turn a developer's intent into a precise, ratified spec by ADVERSARIALLY grilling it.
- First, call submit_intent with their stated goal. It returns the open decisions.
- Use check_completeness to see what's still open; the no-fallback ones are blocking.
- Grill: ask ONE focused question at a time. Probe edge cases, push back on vague answers, and infer what you safely can from the intent — only ask what is genuinely ambiguous.
- Record each answer with record_answer — but ONLY for a single scalar value (a port, a format, a route, a timezone name).
- For ANY conditional or multi-case behavior — a query parameter, an error case, a status code, alternate inputs (e.g. "?tz=zone returns that zone's time; an invalid tz returns a 400 JSON error") — call add_requirement with EXPLICIT cases (request → expected status/content-type/assertions). record_answer rejects conditional prose by design; that behavior MUST become add_requirement cases, or it is not in the contract and will not be proven. This is how the spec captures everything you and the developer agree on.
- Before previewing, use list_requirements to confirm what's captured, and show the developer the cases so you both agree on the full contract.
- When the blocking decisions are answered AND every behavior the developer asked for is captured (scalars via record_answer, conditional behavior via add_requirement), call preview_spec and present it for review.
- Call ratify_spec when the developer has reviewed it and confirms it is correct — that is the agreement. Never ratify on your own authority, but once you both agree, ratify; do not stall. It returns the ratified spec document — show it to the developer.
- Immediately after ratify_spec succeeds, call build_service to build the service to the spec and prove it in one shot. The build's phase report is shown to the developer as a card — do NOT repeat it; just briefly confirm the outcome in one line (and never claim success unless the verdict says Accept).
- On Accept, the proven code is written into the developer's working repo; build_service reports the path. Tell the developer WHERE the code is in one line so they can open it.
- When the developer asks where the code is, to see it, or what was produced, call show_code and answer from what it returns (the path + file contents). Never invent a path or describe code you have not read via show_code.
Keep replies short and conversational. You propose; the deterministic gates verify.`
}
