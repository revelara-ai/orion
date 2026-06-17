package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// fakeLLM is a programmable LLMGenerator for runner tests.
type fakeLLM struct {
	responses []LLMResponse
	err       error
	calls     int
}

func (f *fakeLLM) Generate(_ context.Context, _ LLMRequest) (LLMResponse, error) {
	if f.err != nil {
		return LLMResponse{}, f.err
	}
	if f.calls >= len(f.responses) {
		return LLMResponse{FinishReason: FinishStop}, nil
	}
	r := f.responses[f.calls]
	f.calls++
	return r, nil
}

func buildTestRunner(t *testing.T, gen LLMGenerator) (*LLMRunner, *recordingEventSink, *InMemoryScopeRecorder, *Registry) {
	t.Helper()
	reg := NewRegistry()
	if err := reg.Register(ReadFileTool{Cfg: WorkspaceConfig{WorkspaceRoot: t.TempDir()}}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	sink := NewRecordingEventSink()
	rec := NewInMemoryScopeRecorder()
	runID := uuid.New()
	r, err := NewLLMRunner(LLMRunnerConfig{
		Generator: gen,
		Registry:  reg,
		Sink:      sink,
		Recorder:  rec,
		RunID:     runID,
	})
	if err != nil {
		t.Fatalf("NewLLMRunner: %v", err)
	}
	return r, sink, rec, reg
}

func TestLLMRunner_StartSession_AssignsID(t *testing.T) {
	r, _, _, _ := buildTestRunner(t, &fakeLLM{})
	sid, err := r.StartSession(context.Background(), Prompt{Model: "t", TokenBudget: 1000})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if sid == "" {
		t.Error("StartSession returned empty SessionID")
	}
}

func TestLLMRunner_Turn_EmitsCompleteEvent(t *testing.T) {
	r, sink, _, reg := buildTestRunner(t, &fakeLLM{responses: []LLMResponse{
		{Content: "ok", TokensIn: 5, TokensOut: 10, FinishReason: FinishStop},
	}})
	sid, _ := r.StartSession(context.Background(), Prompt{Model: "t", TokenBudget: 1000})
	out, err := r.Turn(context.Background(), sid, "hi", reg.Definitions())
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if out.FinishReason != FinishStop {
		t.Errorf("FinishReason = %q; want %q", out.FinishReason, FinishStop)
	}
	hasComplete := false
	for _, e := range sink.Events() {
		if e.Kind == EventTurnComplete {
			hasComplete = true
		}
	}
	if !hasComplete {
		t.Error("turn_complete event not emitted")
	}
}

func TestLLMRunner_Turn_DispatchesToolCallsAndRecordsScope(t *testing.T) {
	r, _, rec, reg := buildTestRunner(t, &fakeLLM{responses: []LLMResponse{
		{
			ToolCalls: []ToolCall{
				{Name: "read_file", Args: map[string]any{"path": "src/main.go"}},
				{Name: "read_file", Args: map[string]any{"path": "../etc/passwd"}}, // rejected
			},
			FinishReason: FinishToolUse,
		},
	}})
	sid, _ := r.StartSession(context.Background(), Prompt{Model: "t", TokenBudget: 1000})
	out, err := r.Turn(context.Background(), sid, "do", reg.Definitions())
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if len(out.ToolResults) != 2 {
		t.Fatalf("ToolResults len = %d; want 2", len(out.ToolResults))
	}
	if out.ToolResults[0].Status != ToolAccepted {
		t.Errorf("first dispatch status = %q; want accepted", out.ToolResults[0].Status)
	}
	if out.ToolResults[1].Status != ToolRejected {
		t.Errorf("second dispatch status = %q; want rejected", out.ToolResults[1].Status)
	}
	if rec.CountByTool("read_file") != 2 {
		t.Errorf("CountByTool(read_file) = %d; want 2", rec.CountByTool("read_file"))
	}
	if rec.CountRejections() != 1 {
		t.Errorf("CountRejections = %d; want 1", rec.CountRejections())
	}
}

func TestLLMRunner_Turn_UnknownToolIsRejected(t *testing.T) {
	r, _, _, reg := buildTestRunner(t, &fakeLLM{responses: []LLMResponse{
		{
			ToolCalls:    []ToolCall{{Name: "kubectl_apply", Args: map[string]any{}}}, // not registered
			FinishReason: FinishToolUse,
		},
	}})
	sid, _ := r.StartSession(context.Background(), Prompt{Model: "t", TokenBudget: 1000})
	out, _ := r.Turn(context.Background(), sid, "do", reg.Definitions())
	if len(out.ToolResults) != 1 || out.ToolResults[0].Status != ToolRejected {
		t.Errorf("expected single rejection; got %+v", out.ToolResults)
	}
}

func TestLLMRunner_Turn_BudgetExhaustedReturnsSentinel(t *testing.T) {
	r, _, _, reg := buildTestRunner(t, &fakeLLM{})
	sid, _ := r.StartSession(context.Background(), Prompt{Model: "t", TokenBudget: 10})
	// Manually exhaust the session's tokens to simulate a previous run.
	r.mu.Lock()
	r.sessions[sid].tokensIn = 5
	r.sessions[sid].tokensOut = 5
	r.mu.Unlock()
	_, err := r.Turn(context.Background(), sid, "x", reg.Definitions())
	if !errors.Is(err, ErrTokenBudgetExhausted) {
		t.Errorf("err = %v; want ErrTokenBudgetExhausted", err)
	}
}

func TestLLMRunner_Turn_PropagatesGeneratorError(t *testing.T) {
	r, _, _, reg := buildTestRunner(t, &fakeLLM{err: errors.New("transport boom")})
	sid, _ := r.StartSession(context.Background(), Prompt{Model: "t", TokenBudget: 1000})
	_, err := r.Turn(context.Background(), sid, "x", reg.Definitions())
	if err == nil || err.Error() != "transport boom" {
		t.Errorf("err = %v; want transport boom", err)
	}
}

func TestLLMRunner_Cancel_RejectsFutureTurns(t *testing.T) {
	r, _, _, reg := buildTestRunner(t, &fakeLLM{})
	sid, _ := r.StartSession(context.Background(), Prompt{Model: "t", TokenBudget: 1000})
	if err := r.Cancel(context.Background(), sid); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	_, err := r.Turn(context.Background(), sid, "x", reg.Definitions())
	if err == nil {
		t.Fatal("expected error from Turn after Cancel")
	}
}

func TestLLMRunner_Turn_UnknownSession(t *testing.T) {
	r, _, _, reg := buildTestRunner(t, &fakeLLM{})
	_, err := r.Turn(context.Background(), SessionID("ghost"), "x", reg.Definitions())
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("err = %v; want ErrSessionNotFound", err)
	}
}
