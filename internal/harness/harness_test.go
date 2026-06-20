package harness

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/revelara-ai/orion/internal/budget"
	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/tools"
)

// scriptedProvider returns canned responses in order (staying on the last), and
// records the tool specs it was offered — a deterministic stand-in for a model.
type scriptedProvider struct {
	resp    []*llm.ChatResponse
	i       int
	calls   int
	lastReq llm.ChatRequest
}

func (p *scriptedProvider) Name() string { return "scripted" }
func (p *scriptedProvider) Models(context.Context) ([]llm.ModelInfo, error) {
	return []llm.ModelInfo{{ID: "scripted", Tools: true}}, nil
}
func (p *scriptedProvider) Ping(context.Context) error { return nil }
func (p *scriptedProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.calls++
	p.lastReq = req
	r := p.resp[p.i]
	if p.i < len(p.resp)-1 {
		p.i++
	}
	return r, nil
}

func toolUseResp(id, name, input string) *llm.ChatResponse {
	return &llm.ChatResponse{
		StopReason: llm.StopToolUse,
		Content:    []llm.ContentBlock{{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: id, Name: name, Input: json.RawMessage(input)}}},
		Usage:      llm.Usage{InputTokens: 5, OutputTokens: 5},
	}
}
func endResp(text string) *llm.ChatResponse {
	return &llm.ChatResponse{StopReason: llm.StopEndTurn, Content: []llm.ContentBlock{{Type: llm.BlockText, Text: text}}, Usage: llm.Usage{InputTokens: 3, OutputTokens: 3}}
}

// TestLoopDispatchesToolsThenEnds: the loop runs a tool the model requests, feeds
// the result back, and terminates when the model ends the turn.
func TestLoopDispatchesToolsThenEnds(t *testing.T) {
	reg := tools.NewRegistry()
	var ran bool
	reg.Register(tools.Tool{
		Name: "check_completeness", Description: "what's open",
		Run: func(_ context.Context, in json.RawMessage) (string, error) { ran = true; return "port unanswered", nil },
	})
	prov := &scriptedProvider{resp: []*llm.ChatResponse{
		toolUseResp("t1", "check_completeness", `{}`),
		endResp("Which port should it listen on?"),
	}}
	loop := Loop{Provider: prov, Tools: reg, System: "grill"}

	var kinds []EventKind
	convo, final, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "build a time service")},
		func(e Event) { kinds = append(kinds, e.Kind) })
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !ran {
		t.Fatal("tool was not dispatched")
	}
	if final == nil || final.StopReason != llm.StopEndTurn {
		t.Fatalf("did not end the turn: %+v", final)
	}
	// convo: user, assistant(tool_use), user(tool_result), assistant(text) = 4
	if len(convo) != 4 {
		t.Fatalf("conversation length = %d, want 4: %+v", len(convo), convo)
	}
	if convo[2].Role != llm.RoleUser || convo[2].Content[0].Type != llm.BlockToolResult {
		t.Fatalf("tool result not fed back: %+v", convo[2])
	}
	// the model was offered the tool spec
	if len(prov.lastReq.Tools) != 1 || prov.lastReq.Tools[0].Name != "check_completeness" {
		t.Fatalf("tool spec not offered to the model: %+v", prov.lastReq.Tools)
	}
	_ = kinds
}

// TestSupervisorCapsIterations: a model that never stops requesting tools is
// halted by the iteration cap (no infinite loop).
func TestSupervisorCapsIterations(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{Name: "spin", Run: func(context.Context, json.RawMessage) (string, error) { return "again", nil }})
	prov := &scriptedProvider{resp: []*llm.ChatResponse{toolUseResp("t", "spin", `{}`)}} // always tool_use
	loop := Loop{Provider: prov, Tools: reg, Supervisor: Supervisor{MaxIterations: 3}}
	_, _, err := loop.Run(context.Background(), nil, nil)
	if err != ErrMaxIterations {
		t.Fatalf("err = %v, want ErrMaxIterations", err)
	}
	if prov.calls != 3 {
		t.Fatalf("provider called %d times, want 3 (the cap)", prov.calls)
	}
}

// TestSupervisorHaltsOnBudget: a budget already at its ceiling halts the loop
// before calling the model.
func TestSupervisorHaltsOnBudget(t *testing.T) {
	acct := budget.NewWithCeiling(budget.Ceiling{MaxTokens: 1})
	acct.Record(100, 0) // blow the ceiling
	prov := &scriptedProvider{resp: []*llm.ChatResponse{endResp("hi")}}
	loop := Loop{Provider: prov, Supervisor: Supervisor{Budget: acct}}
	_, _, err := loop.Run(context.Background(), nil, nil)
	if err != ErrBudgetHalt {
		t.Fatalf("err = %v, want ErrBudgetHalt", err)
	}
	if prov.calls != 0 {
		t.Fatal("provider should not be called once the budget is halted")
	}
}
