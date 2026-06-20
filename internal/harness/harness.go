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
	"errors"
	"fmt"

	"github.com/revelara-ai/orion/internal/budget"
	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/tools"
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

	for iter := 0; iter < l.Supervisor.maxIter(); iter++ {
		if err := ctx.Err(); err != nil {
			return convo, nil, err
		}
		if l.Supervisor.Budget != nil && l.Supervisor.Budget.Halted() {
			return convo, nil, ErrBudgetHalt
		}

		resp, err := l.Provider.Chat(ctx, llm.ChatRequest{System: l.System, Messages: convo, Tools: toolSpecs})
		if err != nil {
			return convo, nil, fmt.Errorf("harness: provider: %w", err)
		}
		if l.Supervisor.Budget != nil {
			tok := resp.Usage.InputTokens + resp.Usage.OutputTokens
			l.Supervisor.Budget.Record(tok, float64(tok)*l.CostPerToken)
		}
		if txt := resp.Text(); txt != "" {
			emit(Event{Kind: EventThought, Text: txt})
		}
		// Record the assistant turn (full content, including any tool_use blocks).
		convo = append(convo, llm.Message{Role: llm.RoleAssistant, Content: resp.Content})

		if resp.StopReason != llm.StopToolUse {
			emit(Event{Kind: EventDone})
			return convo, resp, nil
		}

		// Dispatch the requested tools deterministically; feed results back.
		var results []llm.ContentBlock
		for _, tu := range resp.ToolUses() {
			emit(Event{Kind: EventToolCall, Tool: tu.Name})
			content, isErr := l.dispatch(ctx, tu)
			emit(Event{Kind: EventToolResult, Tool: tu.Name, Text: content, Error: isErr})
			results = append(results, llm.ContentBlock{
				Type:       llm.BlockToolResult,
				ToolResult: &llm.ToolResult{ToolUseID: tu.ID, Content: content, IsError: isErr},
			})
		}
		convo = append(convo, llm.Message{Role: llm.RoleUser, Content: results})
	}
	return convo, nil, ErrMaxIterations
}

func (l *Loop) dispatch(ctx context.Context, tu llm.ToolUse) (string, bool) {
	if l.Tools == nil {
		return fmt.Sprintf("no tools registered (requested %q)", tu.Name), true
	}
	return l.Tools.Dispatch(ctx, tu.Name, tu.Input)
}
