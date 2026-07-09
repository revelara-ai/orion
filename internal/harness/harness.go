// Package harness is Orion's native agent loop + supervisor (native-harness
// Phase 2). It runs a provider-agnostic ReAct loop — Chat → (while the model
// requests tools) dispatch tools → feed results back → re-Chat — until the model
// ends the turn. A non-LLM SUPERVISOR owns the hard limits (max iterations,
// token/$ budget, context cancellation): this loop is the single
// interception/audit point, so "no agent grades its own homework" is enforced
// HERE, not inside a provider adapter. Tool dispatch is deterministic; provider
// output never bypasses it.
package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/revelara-ai/orion/internal/budget"
	"github.com/revelara-ai/orion/internal/contextwindow"
	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
)

// keepRecentToolResults is how many of the most recent tool-result bodies stay
// verbatim when the loop clears context proactively — enough for the model to
// see what it just did, while older (re-fetchable) outputs are shed.
const keepRecentToolResults = 3

// Decision is the outcome of an Approve hook for a destructive tool.
type Decision int

const (
	DecisionAllow Decision = iota // run the tool
	DecisionDeny                  // do not run it; tell the model
)

// EventKind classifies a streamed loop event (rendered by the TUI).
type EventKind string

const (
	EventThought    EventKind = "thought"     // assistant text
	EventToolCall   EventKind = "tool_call"   // a tool is about to run
	EventToolResult EventKind = "tool_result" // a tool returned
	EventDone       EventKind = "done"        // the turn ended
)

// Event is a streamed step of a turn.
type Event struct {
	Kind  EventKind
	Text  string
	Tool  string
	Error bool
}

// Supervisor bounds a turn: iteration cap, budget halt, and (via ctx) the kill
// switch. Zero MaxIterations means a sane default.
type Supervisor struct {
	MaxIterations int
	Budget        *budget.Accountant
}

func (s Supervisor) maxIter() int {
	if s.MaxIterations > 0 {
		return s.MaxIterations
	}
	return 24
}

var (
	// ErrMaxIterations: the loop hit the supervisor's iteration cap.
	ErrMaxIterations = errors.New("harness: max iterations exceeded")
	// ErrBudgetHalt: the budget accountant reached its ceiling.
	ErrBudgetHalt = errors.New("harness: budget ceiling reached")
)

// Loop is a configured agent loop.
type Loop struct {
	Provider   llm.Provider
	Tools      *tools.Registry
	System     string
	Supervisor Supervisor
	// CostPerToken optionally prices tokens for the budget (dollars). 0 = tokens only.
	CostPerToken float64
	// Approve (may be nil) is consulted before dispatching a tool whose Safety
	// RequiresApproval (acts in the developer's environment). On DecisionDeny the tool is
	// NOT run and a denial message is fed back to the model so it adapts. Other tools —
	// including internal Destructive spec/change tools — never consult it. Subagents
	// leave it nil (headless).
	Approve func(ctx context.Context, name string, input json.RawMessage, safety tools.Safety) Decision
	// MaxTokens is the DESIRED per-response output cap. The loop requests this much
	// room (clamped to fit the window), so a generation turn can write a full file —
	// the provider's tiny default (4096) truncates a write_file tool input mid-JSON.
	// 0 uses defaultMaxTokens.
	MaxTokens int
}

const (
	// defaultMaxTokens is the output room the loop requests when MaxTokens is unset —
	// generous enough to write a full file in one turn (the 4096 provider default is
	// not). It is a ceiling, not a target: you pay for tokens actually generated.
	defaultMaxTokens = 32000
	// maxTokensFloor is the minimum we'll ever request, and outputMargin keeps input
	// + requested output safely under the window (structural/estimate slack).
	maxTokensFloor = 1024
	outputMargin   = 2048
)

// clampMaxTokens returns as much output room as desired while guaranteeing input +
// output stays under the window (a request whose max_tokens + input exceeds the
// window is itself rejected by the provider). Floored so it's always a valid cap.
func clampMaxTokens(desired, window, inputTokens int) int {
	if room := window - inputTokens - outputMargin; room < desired {
		desired = room
	}
	if desired < maxTokensFloor {
		return maxTokensFloor
	}
	return desired
}

func (l *Loop) desiredMaxTokens() int {
	desired := defaultMaxTokens
	if l.MaxTokens > 0 {
		desired = l.MaxTokens
	}
	// Never request more output than the model actually allows — the provider
	// rejects an over-cap max_tokens with an unrecoverable 400. Providers that don't
	// report a cap use a safe default.
	if cap := providerMaxOutput(l.Provider); cap < desired {
		desired = cap
	}
	return desired
}

// defaultProviderMaxOutput bounds max_tokens for a provider that doesn't advertise
// its output cap (offline brain, future adapters) — safe for essentially any model.
const defaultProviderMaxOutput = 8192

func providerMaxOutput(prov llm.Provider) int {
	if mo, ok := prov.(llm.MaxOutputTokens); ok {
		if n := mo.MaxOutputTokens(); n > 0 {
			return n
		}
	}
	return defaultProviderMaxOutput
}

// Run advances the conversation by one developer turn: it sends the conversation
// to the model and resolves any tool calls until the model ends the turn. It
// returns the extended conversation (assistant + tool_result messages appended)
// and the terminal response. onEvent (may be nil) streams incremental steps.
func (l *Loop) Run(ctx context.Context, convo []llm.Message, onEvent func(Event)) ([]llm.Message, *llm.ChatResponse, error) {
	emit := func(e Event) {
		if onEvent != nil {
			onEvent(e)
		}
	}
	var toolSpecs []llm.Tool
	if l.Tools != nil {
		toolSpecs = l.Tools.Specs()
	}
	// Context-window policy for THIS provider: thresholds are fractions of the
	// model's window (per-model via the optional llm.ContextWindow capability, else
	// a conservative default), so the same code bounds a 1M Anthropic brain and an
	// 8K local one. Providers that can't report a window degrade gracefully.
	policy := contextwindow.For(contextwindow.WindowOf(l.Provider, contextwindow.DefaultWindow))
	var stall stallTracker

	for iter := 0; iter < l.Supervisor.maxIter(); iter++ {
		if err := ctx.Err(); err != nil {
			return convo, nil, err
		}
		if l.Supervisor.Budget != nil && l.Supervisor.Budget.Halted() {
			return convo, nil, ErrBudgetHalt
		}

		// Two proactive levers, cheapest first, both shedding old tool-result BODIES
		// (re-fetchable from disk) and persisting the shrunk convo forward:
		//   1. gentle — at ClearAt, keep the context lean but protect the recent results;
		//   2. hard GUARD — never knowingly send above GuardAt; shed aggressively,
		//      protecting only the newest result.
		req := llm.ChatRequest{System: l.System, Messages: convo, Tools: toolSpecs}
		if llm.EstimateTokens(req) > policy.ClearAt {
			convo = contextwindow.Fit(req, policy.ClearAt, keepRecentToolResults)
			req.Messages = convo
		}
		if llm.EstimateTokens(req) > policy.GuardAt {
			convo = contextwindow.Fit(req, policy.GuardAt, 1)
			req.Messages = convo
		}

		// Request generous output room (clamped to fit the window on top of the input),
		// so a generation turn can write a FULL file — the provider's 4096 default
		// truncates a large write_file tool input mid-JSON.
		req.MaxTokens = clampMaxTokens(l.desiredMaxTokens(), policy.Window, llm.EstimateTokens(req))

		// Stream the turn: text deltas surface live as EventThought; the assembled
		// response (incl. tool_use) comes back so tool dispatch is unchanged. The
		// deltas ARE the thought stream, so we don't re-emit resp.Text() afterward.
		onDelta := func(delta string) { emit(Event{Kind: EventThought, Text: delta}) }
		resp, err := l.Provider.ChatStream(ctx, req, onDelta)
		if errors.Is(err, llm.ErrContextOverflow) {
			// The provider's exact count exceeded our estimate. Shed EVERYTHING
			// clearable (keepRecent=0 — even the newest result may be the offender) down
			// to a target below the current size, then retry ONCE. But if clearing can't
			// reduce it (dialogue/text dominated), don't re-send an identical prompt —
			// surface the overflow; compaction (a later slice) is the lever there.
			before := llm.EstimateTokens(req)
			target := policy.GuardAt
			if half := before / 2; half < target {
				target = half // shed aggressively when we can
			}
			convo = contextwindow.Fit(req, target, 0)
			req.Messages = convo
			after := llm.EstimateTokens(req)
			// Retry ONCE only if clearing made progress AND brought the prompt under
			// GuardAt (a margin below the window that leaves room for the response).
			// If it made no progress, or an irreducible text/dialogue floor still
			// exceeds GuardAt, re-sending would just re-overflow — surface the error
			// (compaction, a later slice, is the lever for an over-window floor).
			if after < before && after <= policy.GuardAt {
				emit(Event{Kind: EventThought, Text: "\n[context full — cleared old tool output, retrying]\n"})
				resp, err = l.Provider.ChatStream(ctx, req, onDelta)
			}
		}
		if err != nil {
			return convo, nil, fmt.Errorf("harness: provider: %w", err)
		}
		if l.Supervisor.Budget != nil {
			tok := resp.Usage.InputTokens + resp.Usage.OutputTokens
			l.Supervisor.Budget.Record(tok, float64(tok)*l.CostPerToken)
		}
		// Record the assistant turn (full content, including any tool_use blocks).
		// Skip a degenerate empty turn — an empty content array can't be re-sent to
		// the provider (it serializes to null) and carries no information.
		if len(resp.Content) > 0 {
			convo = append(convo, llm.Message{Role: llm.RoleAssistant, Content: resp.Content})
		}

		if resp.StopReason != llm.StopToolUse {
			emit(Event{Kind: EventDone})
			return convo, resp, nil
		}

		// Dispatch the requested tools deterministically; feed results back.
		var results []llm.ContentBlock
		for _, tu := range resp.ToolUses() {
			emit(Event{Kind: EventToolCall, Tool: tu.Name})
			// Stall detector (or-mvr adjunct): a model repeating the IDENTICAL
			// call is in a loop the result will never break — nudge it off the
			// track instead of executing, and cleanly stop the turn if it
			// persists (a named stop beats grinding to max-iterations).
			if stall.observe(tu.Name, tu.Input) >= stallAbortAt {
				return convo, resp, fmt.Errorf("harness: %w — %s repeated %d× with identical input; the turn was stopped so the loop can regroup", ErrStalled, tu.Name, stallAbortAt)
			}
			var content string
			var isErr bool
			if stall.count >= stallNudgeAt {
				content = fmt.Sprintf("stall detected: this exact %s call has now been made %d times in a row and the result will not change. Do NOT repeat it. Change the input, take a different approach, or end your turn and explain the blocker to the developer.", tu.Name, stall.count)
				isErr = true
			} else {
				content, isErr = l.dispatch(ctx, tu)
			}
			emit(Event{Kind: EventToolResult, Tool: tu.Name, Text: content, Error: isErr})
			results = append(results, llm.ContentBlock{
				Type:       llm.BlockToolResult,
				ToolResult: &llm.ToolResult{ToolUseID: tu.ID, Content: content, IsError: isErr},
			})
		}
		if len(results) == 0 {
			// stop_reason=tool_use but no dispatchable tool call — don't emit an empty
			// user turn; surface it instead of silently corrupting the conversation.
			return convo, resp, fmt.Errorf("harness: provider signaled tool_use but produced no tool calls")
		}
		convo = append(convo, llm.Message{Role: llm.RoleUser, Content: results})
	}
	return convo, nil, ErrMaxIterations
}

func (l *Loop) dispatch(ctx context.Context, tu llm.ToolUse) (string, bool) {
	if l.Tools == nil {
		return fmt.Sprintf("no tools registered (requested %q)", tu.Name), true
	}
	// Gate a tool that acts in the developer's environment on the approval hook (when
	// set). A denial short-circuits dispatch and tells the model — it never crashes, it
	// adapts. Internal state-mutating tools (Destructive but not RequiresApproval) are
	// NOT gated.
	if l.Approve != nil {
		if t, ok := l.Tools.Get(tu.Name); ok && t.Safety.RequiresApproval {
			if l.Approve(ctx, tu.Name, tu.Input, t.Safety) == DecisionDeny {
				return "The user denied permission to run " + tu.Name + "; do not retry it — adapt or ask them what to do instead.", true
			}
		}
	}
	return l.Tools.Dispatch(ctx, tu.Name, tu.Input)
}
