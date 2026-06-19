package conductor

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

// ConductorAgent is the Conductor exposed as an ACP agent (SPEC §3): primed by a
// role template, it runs the prompt-turn conversation that narrows an intent into
// a ratified spec — the completeness "agent skill" lives HERE, server-side, so
// the TUI is a thin ACP client (or-owz). It reasons/coordinates only; proof, the
// deployment bar, and leases stay deterministic tools it invokes, never overrides.
//
// In production the reasoning is the spawned vendor agent; this Go agent is the
// reference/headless Conductor and the seam the TUI drives.
type ConductorAgent struct {
	Role      RoleTemplate
	conductor *orchestrator.Conductor

	mu       sync.Mutex
	sessions map[string]*convoState
}

// convoState is per-ACP-session conversation progress.
type convoState struct {
	started bool                        // intent submitted?
	pending []completeness.OpenDecision // blocking questions still to answer
}

// NewConductorAgent builds a Conductor agent backed by the orchestrator Conductor
// (which owns the store + completeness gate). The backing conductor is what makes
// the questioning a real skill rather than a canned echo.
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

// Prompt runs one turn of the intent → grill → ready conversation. The first
// prompt is the intent; each subsequent prompt answers the current blocking
// question. One question is streamed per turn (with its dimension + remaining
// count) so the developer always knows what to answer.
func (ca *ConductorAgent) Prompt(ctx context.Context, sessionID, text string, stream func(acp.Update)) (acp.PromptResult, error) {
	end := acp.PromptResult{StopReason: "end_turn"}
	if ca.conductor == nil {
		stream(acp.Update{Kind: "agent_message", Text: "conductor backend not configured"})
		return end, nil
	}
	st := ca.sessionState(sessionID)

	// First turn: the intent.
	if !st.started {
		conf, err := ca.conductor.Submit(ctx, text)
		if err != nil {
			stream(acp.Update{Kind: "agent_message", Text: "I can't take that yet: " + err.Error()})
			return end, nil
		}
		st.started = true
		stream(acp.Update{Kind: "agent_thought", Text: conf.Message})
		st.pending = blockingOpen(conf.OpenDecisions)
		if len(st.pending) == 0 {
			return ca.finalize(ctx, stream)
		}
		ca.askOne(st, stream)
		return end, nil
	}

	// Already ratified: guide toward the build.
	if len(st.pending) == 0 {
		stream(acp.Update{Kind: "agent_message", Text: "Spec is ratified — run `orion run` to build, or start a new intent."})
		return end, nil
	}

	// Otherwise: this prompt answers the current blocking question.
	od := st.pending[0]
	if strings.TrimSpace(text) == "" {
		stream(acp.Update{Kind: "agent_message", Text: "That one needs an answer — " + od.Question})
		return end, nil
	}
	if err := ca.conductor.RecordAnswer(ctx, od.Key, text); err != nil {
		stream(acp.Update{Kind: "agent_message", Text: "couldn't record that: " + err.Error()})
		return end, nil
	}
	// Recompute remaining blocking questions from the persisted answers.
	if sv, err := ca.conductor.SpecView(ctx); err == nil {
		st.pending = blockingOpen(sv.OpenDecisions)
	} else if len(st.pending) > 0 {
		st.pending = st.pending[1:]
	}
	if len(st.pending) == 0 {
		return ca.finalize(ctx, stream)
	}
	ca.askOne(st, stream)
	return end, nil
}

// askOne streams the current question — one at a time, with guidance.
func (ca *ConductorAgent) askOne(st *convoState, stream func(acp.Update)) {
	od := st.pending[0]
	stream(acp.Update{Kind: "agent_message", Text: fmt.Sprintf("[%s] %s   (%d to answer)", od.Dimension, od.Question, len(st.pending))})
}

// finalize ratifies the spec (fallback-eligible dimensions resolve to presets)
// and streams the plan/ready signal.
func (ca *ConductorAgent) finalize(ctx context.Context, stream func(acp.Update)) (acp.PromptResult, error) {
	es, err := ca.conductor.ApproveSpec(ctx)
	if err != nil {
		stream(acp.Update{Kind: "agent_message", Text: "I can't finalize the spec yet: " + err.Error()})
		return acp.PromptResult{StopReason: "end_turn"}, nil
	}
	stream(acp.Update{Kind: "plan", Text: fmt.Sprintf("Spec ratified ✓  route=%s  format=%s  (hash %s) — run `orion run` to build.",
		es.ResponseContract.Route, es.Decisions["response_format"], shortHash(es.Hash))})
	return acp.PromptResult{StopReason: "end_turn"}, nil
}

func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// blockingOpen keeps only decisions with no safe default (the developer must
// answer these); fallback-eligible decisions resolve to presets at approve time.
func blockingOpen(open []completeness.OpenDecision) []completeness.OpenDecision {
	var b []completeness.OpenDecision
	for _, od := range open {
		if od.Fallback == "" {
			b = append(b, od)
		}
	}
	return b
}
