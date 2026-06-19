package conductor

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// ConductorAgent is the Conductor exposed as an ACP agent (SPEC §3): primed by a
// role template, it runs the conversation that narrows an intent into a ratified
// spec — the completeness "agent skill" lives HERE, server-side. After the
// blocking questions it PRESENTS the assembled spec for review and ratifies only
// on the developer's say-so (or-owz, or-owo). It reasons/coordinates only; proof,
// the deployment bar, and leases stay deterministic tools it invokes.
type ConductorAgent struct {
	Role      RoleTemplate
	conductor *orchestrator.Conductor

	mu       sync.Mutex
	sessions map[string]*convoState
}

// convoState is per-ACP-session conversation progress.
type convoState struct {
	started        bool                        // intent submitted?
	pending        []completeness.OpenDecision // blocking questions still to answer
	awaitingRatify bool                        // spec presented, awaiting ratify/edit
	ratified       bool
}

// NewConductorAgent builds a Conductor agent backed by the orchestrator Conductor
// (which owns the store + completeness gate).
func NewConductorAgent(role RoleTemplate, c *orchestrator.Conductor) *ConductorAgent {
	return &ConductorAgent{Role: role, conductor: c, sessions: map[string]*convoState{}}
}

// Serve runs the Conductor as an ACP agent over the given transport.
func (ca *ConductorAgent) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	return acp.NewAgent(r, w, ca.Prompt).Run(ctx)
}

func (ca *ConductorAgent) sessionState(sid string) *convoState {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	st := ca.sessions[sid]
	if st == nil {
		st = &convoState{}
		ca.sessions[sid] = st
	}
	return st
}

// Prompt runs one turn of the intent → grill → review → ratified conversation.
func (ca *ConductorAgent) Prompt(ctx context.Context, sessionID, text string, stream func(acp.Update), ask acp.AskFunc) (acp.PromptResult, error) {
	end := acp.PromptResult{StopReason: "end_turn"}
	if ca.conductor == nil {
		stream(acp.Update{Kind: "agent_message", Text: "conductor backend not configured"})
		return end, nil
	}
	st := ca.sessionState(sessionID)

	switch {
	case !st.started:
		return ca.handleIntent(ctx, st, text, stream, ask), nil
	case st.ratified:
		stream(acp.Update{Kind: "agent_message", Text: "Spec is ratified — run `orion run` to build, or start a new intent."})
		return end, nil
	case st.awaitingRatify:
		return ca.handleReview(ctx, st, text, stream), nil
	default:
		return ca.handleAnswer(ctx, st, text, stream, ask), nil
	}
}

// handleIntent: the first turn submits the intent and either asks the first
// blocking question or presents the spec for review.
func (ca *ConductorAgent) handleIntent(ctx context.Context, st *convoState, text string, stream func(acp.Update), ask acp.AskFunc) acp.PromptResult {
	end := acp.PromptResult{StopReason: "end_turn"}
	conf, err := ca.conductor.Submit(ctx, text)
	if err != nil {
		stream(acp.Update{Kind: "agent_message", Text: "I can't take that yet: " + err.Error()})
		return end
	}
	st.started = true
	stream(acp.Update{Kind: "agent_thought", Text: conf.Message})
	st.pending = blockingOpen(conf.OpenDecisions)
	if len(st.pending) == 0 {
		ca.presentSpec(ctx, st, stream, ask)
		return end
	}
	ca.askOne(st, stream)
	return end
}

// handleAnswer records the answer to the current question and advances.
func (ca *ConductorAgent) handleAnswer(ctx context.Context, st *convoState, text string, stream func(acp.Update), ask acp.AskFunc) acp.PromptResult {
	end := acp.PromptResult{StopReason: "end_turn"}
	od := st.pending[0]
	if strings.TrimSpace(text) == "" {
		stream(acp.Update{Kind: "agent_message", Text: "That one needs an answer — " + od.Question})
		return end
	}
	if err := ca.conductor.RecordAnswer(ctx, od.Key, text); err != nil {
		stream(acp.Update{Kind: "agent_message", Text: "couldn't record that: " + err.Error()})
		return end
	}
	if sv, err := ca.conductor.SpecView(ctx); err == nil {
		st.pending = blockingOpen(sv.OpenDecisions)
	} else if len(st.pending) > 0 {
		st.pending = st.pending[1:]
	}
	if len(st.pending) == 0 {
		ca.presentSpec(ctx, st, stream, ask)
		return end
	}
	ca.askOne(st, stream)
	return end
}

// handleReview processes the developer's response to the presented spec: ratify,
// or edit a field and re-review.
func (ca *ConductorAgent) handleReview(ctx context.Context, st *convoState, text string, stream func(acp.Update)) acp.PromptResult {
	end := acp.PromptResult{StopReason: "end_turn"}
	reply := strings.TrimSpace(text)
	switch strings.ToLower(reply) {
	case "y", "yes", "ratify", "approve":
		ca.ratify(ctx, st, stream)
		return end
	}

	// Otherwise treat it as an edit: "<field> <value>" or "edit <field> <value>".
	fields := strings.Fields(reply)
	if len(fields) > 0 && strings.ToLower(fields[0]) == "edit" {
		fields = fields[1:]
	}
	if len(fields) < 2 {
		stream(acp.Update{Kind: "agent_message", Text: "Reply 'y' to ratify, or '<field> <value>' to change one (e.g. 'port 9090')."})
		return end
	}
	key, value := fields[0], strings.Join(fields[1:], " ")
	if !ca.conductor.DecisionKeys()[key] {
		stream(acp.Update{Kind: "agent_message", Text: fmt.Sprintf("'%s' isn't a spec field. Reply 'y' to ratify, or '<field> <value>' to change one.", key)})
		return end
	}
	if err := ca.conductor.RecordAnswer(ctx, key, value); err != nil {
		stream(acp.Update{Kind: "agent_message", Text: "couldn't record that: " + err.Error()})
		return end
	}
	st.awaitingRatify = false
	if sv, err := ca.conductor.SpecView(ctx); err == nil {
		st.pending = blockingOpen(sv.OpenDecisions)
	}
	if len(st.pending) > 0 {
		ca.askOne(st, stream)
	} else {
		ca.presentSpec(ctx, st, stream, nil) // conversational re-present (next-turn, no live gate)
	}
	return end
}

// askOne streams the current question — one at a time, with guidance.
func (ca *ConductorAgent) askOne(st *convoState, stream func(acp.Update)) {
	od := st.pending[0]
	stream(acp.Update{Kind: "agent_message", Text: fmt.Sprintf("[%s] %s   (%d to answer)", od.Dimension, od.Question, len(st.pending))})
}

// presentSpec assembles the spec (without accepting it) and streams it for
// review, then seeks ratification. With a client permission gate (the TUI), it
// uses a blocking session/request_permission(spec_ratify) — the developer
// authorizes in-place. Without a gate (headless / no UI), it falls back to a
// conversational ratify ('y' / '<field> <value>') handled on the next turn.
func (ca *ConductorAgent) presentSpec(ctx context.Context, st *convoState, stream func(acp.Update), ask acp.AskFunc) {
	es, err := ca.conductor.PreviewSpec(ctx)
	if err != nil {
		stream(acp.Update{Kind: "agent_message", Text: "I can't assemble the spec yet: " + err.Error()})
		return
	}
	stream(acp.Update{Kind: "spec", Text: formatSpecCard(es)})

	if ask != nil {
		res, aerr := ask(acp.PermissionRequest{Kind: "spec_ratify", Title: "Ratify the assembled spec?"})
		if aerr == nil {
			if res.Outcome == "granted" {
				ca.ratify(ctx, st, stream)
				return
			}
			// Declined → let the developer change a field, then we re-present.
			stream(acp.Update{Kind: "agent_message", Text: "Not ratified. Reply '<field> <value>' to change one (e.g. 'port 9090'), or 'y' to ratify."})
			st.awaitingRatify = true
			return
		}
		// ask errored (no gate configured) → conversational fallback below.
	}
	stream(acp.Update{Kind: "agent_message", Text: "Review the spec above. Reply 'y' to ratify, or '<field> <value>' to change one (e.g. 'port 9090')."})
	st.awaitingRatify = true
}

// ratify accepts the spec and streams the ready signal.
func (ca *ConductorAgent) ratify(ctx context.Context, st *convoState, stream func(acp.Update)) {
	es, err := ca.conductor.ApproveSpec(ctx)
	if err != nil {
		stream(acp.Update{Kind: "agent_message", Text: "I can't finalize the spec yet: " + err.Error()})
		return
	}
	st.awaitingRatify = false
	st.ratified = true
	stream(acp.Update{Kind: "plan", Text: fmt.Sprintf("Spec ratified ✓  route=%s  format=%s  (hash %s) — run `orion run` to build.",
		es.ResponseContract.Route, es.Decisions["response_format"], shortHash(es.Hash))})
}

// formatSpecCard renders the assembled spec for developer review — dimension
// values with defaults marked, plus the response contract.
func formatSpecCard(es spec.ExecutableSpec) string {
	var b strings.Builder
	for _, d := range es.Dimensions {
		keys := make([]string, 0, len(d.Values))
		for k := range d.Values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, k+"="+d.Values[k])
		}
		mark := ""
		if d.ValueKind == "fallback_preset" {
			mark = "  (default)"
		}
		b.WriteString(fmt.Sprintf("%-13s %s%s\n", string(d.Name), strings.Join(parts, ", "), mark))
	}
	b.WriteString(fmt.Sprintf("contract      GET %s → %s", es.ResponseContract.Route, es.Decisions["response_format"]))
	return b.String()
}

func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// blockingOpen keeps only decisions with no safe default.
func blockingOpen(open []completeness.OpenDecision) []completeness.OpenDecision {
	var b []completeness.OpenDecision
	for _, od := range open {
		if od.Fallback == "" {
			b = append(b, od)
		}
	}
	return b
}
