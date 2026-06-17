package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// AgentRunner is the SPEC §11.2 contract. Implementations dispatch
// tool calls via the supplied Registry and record every dispatch with
// the supplied ScopeRecorder. Implementations MUST be safe to call
// from multiple goroutines and MUST update last_event_at on each
// emitted event so the Lookout can detect stalls.
type AgentRunner interface {
	StartSession(ctx context.Context, prompt Prompt) (SessionID, error)
	Turn(ctx context.Context, sid SessionID, userMsg string, tools []ToolDef) (TurnResult, error)
	Cancel(ctx context.Context, sid SessionID) error
}

// LLMGenerator is the narrow contract the runner uses to call out to
// a large language model. Concrete implementations bind
// internal/llm.Generator at the worker level; unit tests inject fakes
// so the runner can be exercised without an LLM.
type LLMGenerator interface {
	Generate(ctx context.Context, req LLMRequest) (LLMResponse, error)
}

// LLMRequest is the minimal request shape the runner needs. The
// concrete adapter (cmd/orion-worker/main.go) maps this to
// internal/llm.GenerateRequest.
type LLMRequest struct {
	Model     string
	System    string
	History   []LLMMessage
	Tools     []ToolDef
	MaxTokens int
}

// LLMMessage is one prior-turn message.
type LLMMessage struct {
	Role    string // "user" | "assistant" | "tool"
	Content string
	// ToolCallID, when set, identifies a tool-result message tied to a
	// specific prior tool_call_requested event.
	ToolCallID string
}

// LLMResponse is the structured outcome of a Generate call.
type LLMResponse struct {
	Content      string
	ToolCalls    []ToolCall
	TokensIn     int
	TokensOut    int
	FinishReason FinishReason
}

// LLMRunner is the production AgentRunner backed by an LLMGenerator.
// Sessions are kept in-process; the worker only ever has one or two
// in flight per pod so this is fine.
type LLMRunner struct {
	gen      LLMGenerator
	registry *Registry
	sink     EventSink
	recorder ScopeRecorder
	runID    uuid.UUID
	claimID  *uuid.UUID
	wsID     *uuid.UUID

	mu       sync.Mutex
	sessions map[SessionID]*sessionState
}

type sessionState struct {
	prompt    Prompt
	history   []LLMMessage
	tokensIn  int
	tokensOut int
	cancel    context.CancelFunc
	cancelled bool
}

// LLMRunnerConfig parameterizes NewLLMRunner.
type LLMRunnerConfig struct {
	Generator       LLMGenerator
	Registry        *Registry
	Sink            EventSink
	Recorder        ScopeRecorder
	RunID           uuid.UUID
	ClaimID         *uuid.UUID
	WorkerSessionID *uuid.UUID
}

// NewLLMRunner builds an LLMRunner. The registry, sink, and recorder
// MUST be non-nil; the runner refuses to run without all three.
func NewLLMRunner(cfg LLMRunnerConfig) (*LLMRunner, error) {
	if cfg.Generator == nil {
		return nil, errors.New("agent: nil LLMGenerator")
	}
	if cfg.Registry == nil {
		return nil, errors.New("agent: nil Registry")
	}
	if cfg.Sink == nil {
		return nil, errors.New("agent: nil EventSink")
	}
	if cfg.Recorder == nil {
		return nil, errors.New("agent: nil ScopeRecorder")
	}
	if cfg.RunID == uuid.Nil {
		return nil, errors.New("agent: RunID required")
	}
	return &LLMRunner{
		gen:      cfg.Generator,
		registry: cfg.Registry,
		sink:     cfg.Sink,
		recorder: cfg.Recorder,
		runID:    cfg.RunID,
		claimID:  cfg.ClaimID,
		wsID:     cfg.WorkerSessionID,
		sessions: map[SessionID]*sessionState{},
	}, nil
}

// StartSession registers a new session.
func (r *LLMRunner) StartSession(_ context.Context, prompt Prompt) (SessionID, error) {
	sid := SessionID(uuid.NewString())
	r.mu.Lock()
	r.sessions[sid] = &sessionState{prompt: prompt}
	r.mu.Unlock()
	return sid, nil
}

// Turn runs one Turn against the session. It enforces the per-session
// token budget (§11.4 #3), dispatches tool calls through the
// Registry (with structural enforcement at each tool), records a
// ScopeRequest for every dispatch, and emits incremental events.
func (r *LLMRunner) Turn(ctx context.Context, sid SessionID, userMsg string, tools []ToolDef) (TurnResult, error) {
	r.mu.Lock()
	s, ok := r.sessions[sid]
	r.mu.Unlock()
	if !ok {
		return TurnResult{}, ErrSessionNotFound
	}
	if s.cancelled {
		return TurnResult{}, fmt.Errorf("agent: session cancelled")
	}
	if s.prompt.TokenBudget > 0 && s.tokensIn+s.tokensOut >= s.prompt.TokenBudget {
		return TurnResult{SessionID: sid, FinishReason: FinishBudgetExhausted}, ErrTokenBudgetExhausted
	}

	turnCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	s.cancel = cancel
	r.mu.Unlock()
	defer cancel()

	s.history = append(s.history, LLMMessage{Role: "user", Content: userMsg})
	r.emit(turnCtx, Event{Kind: EventTokensInProgress, SessionID: sid, Payload: map[string]any{"len": len(userMsg)}})

	resp, err := r.gen.Generate(turnCtx, LLMRequest{
		Model:     s.prompt.Model,
		System:    s.prompt.System,
		History:   s.history,
		Tools:     tools,
		MaxTokens: s.prompt.TokenBudget,
	})
	if err != nil {
		return TurnResult{SessionID: sid, FinishReason: FinishError}, err
	}
	s.tokensIn += resp.TokensIn
	s.tokensOut += resp.TokensOut

	out := TurnResult{
		SessionID:    sid,
		FinishReason: resp.FinishReason,
		TokensIn:     resp.TokensIn,
		TokensOut:    resp.TokensOut,
		Response:     resp.Content,
		ToolCalls:    resp.ToolCalls,
	}

	for _, call := range resp.ToolCalls {
		r.emit(turnCtx, Event{Kind: EventToolCallRequested, SessionID: sid, Payload: map[string]any{"name": call.Name}})
		result := r.dispatchTool(turnCtx, call)
		out.ToolResults = append(out.ToolResults, result)
		r.emit(turnCtx, Event{Kind: EventToolResult, SessionID: sid, Payload: map[string]any{"name": call.Name, "status": string(result.Status)}})

		var reason *string
		if result.Status != ToolAccepted {
			rs := result.RejectReason
			reason = &rs
		}
		recErr := r.recorder.Record(turnCtx, ScopeRecord{
			RunID:           r.runID,
			ClaimID:         r.claimID,
			WorkerSessionID: r.wsID,
			ToolName:        call.Name,
			RequestedScope:  call.Args,
			GrantedScope:    result.Result,
			RejectionReason: reason,
		})
		if recErr != nil {
			// Recording failure is auditable but does not abort the
			// Turn; the operator-facing escalation review surfaces the
			// gap. The runner emits an error event so the Lookout sees
			// the issue.
			r.emit(turnCtx, Event{Kind: EventToolResult, SessionID: sid, Payload: map[string]any{"name": call.Name, "record_error": recErr.Error()}})
		}

		// Append the tool result to history so the next Turn sees it.
		s.history = append(s.history, LLMMessage{
			Role:       "tool",
			ToolCallID: call.Name,
			Content:    summarizeToolResult(result),
		})
	}

	if resp.Content != "" {
		s.history = append(s.history, LLMMessage{Role: "assistant", Content: resp.Content})
	}
	r.emit(turnCtx, Event{Kind: EventTurnComplete, SessionID: sid, Payload: map[string]any{
		"finish_reason": string(out.FinishReason),
		"tokens_in":     out.TokensIn,
		"tokens_out":    out.TokensOut,
	}})
	return out, nil
}

// Cancel terminates an in-progress session. Future Turn calls return
// "session cancelled".
func (r *LLMRunner) Cancel(_ context.Context, sid SessionID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[sid]
	if !ok {
		return ErrSessionNotFound
	}
	s.cancelled = true
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

func (r *LLMRunner) dispatchTool(ctx context.Context, call ToolCall) ToolResult {
	tool, ok := r.registry.Get(call.Name)
	if !ok {
		return ToolResult{Status: ToolRejected, RejectReason: fmt.Sprintf("tool %q is not registered", call.Name)}
	}
	return tool.Execute(ctx, call.Args)
}

func (r *LLMRunner) emit(ctx context.Context, e Event) {
	e.OccurredAt = time.Now()
	if err := r.sink.Emit(ctx, e); err != nil {
		// Emit failure is non-fatal: the runner continues. The Lookout
		// will notice the missing heartbeat and react.
		_ = err
	}
}

// summarizeToolResult builds the short tool-result message we append
// to history. We deliberately keep this terse; the SPEC §11.4
// continuation prompts are supposed to be short to avoid token waste.
func summarizeToolResult(r ToolResult) string {
	switch r.Status {
	case ToolAccepted:
		return "accepted"
	case ToolRejected:
		return "rejected: " + r.RejectReason
	case ToolErrored:
		return "errored: " + r.RejectReason
	default:
		return "unknown"
	}
}
