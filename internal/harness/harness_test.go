package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/budget"
	"github.com/revelara-ai/orion/internal/contextwindow"
	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
)

// bigResultMsg is a user turn carrying one bulky tool_result body.
func bigResultMsg(id string, chars int) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{
		Type: llm.BlockToolResult, ToolResult: &llm.ToolResult{ToolUseID: id, Content: strings.Repeat("x", chars)},
	}}}
}

// toolUseMsg is an assistant turn requesting a tool (small; survives clearing).
func toolUseMsg(id string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{
		Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: id, Name: "read_file", Input: json.RawMessage(`{}`)},
	}}}
}

// windowProvider advertises a context window and records each request's estimated
// size, but NEVER overflows — used to assert the harness shrinks below GuardAt
// BEFORE sending (the proactive hard guard), independent of reactive recovery.
type windowProvider struct {
	window        int
	lastReqTokens int
	calls         int
}

func (p *windowProvider) Name() string                                    { return "window" }
func (p *windowProvider) ContextWindow() int                              { return p.window }
func (p *windowProvider) Models(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (p *windowProvider) Ping(context.Context) error                      { return nil }
func (p *windowProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	return p.ChatStream(ctx, req, func(string) {})
}
func (p *windowProvider) ChatStream(_ context.Context, req llm.ChatRequest, _ func(string)) (*llm.ChatResponse, error) {
	p.calls++
	p.lastReqTokens = llm.EstimateTokens(req)
	return endResp("ok"), nil
}

// thresholdProvider models a REAL provider: it rejects any request whose estimate
// exceeds its window with ErrContextOverflow (as the real Anthropic streaming path
// now does), else succeeds. This exercises the end-to-end clear+recover wiring
// without a hand-fabricated sentinel.
type thresholdProvider struct {
	window    int
	calls     int
	reqTokens []int
}

func (p *thresholdProvider) Name() string                                    { return "threshold" }
func (p *thresholdProvider) ContextWindow() int                              { return p.window }
func (p *thresholdProvider) Models(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (p *thresholdProvider) Ping(context.Context) error                      { return nil }
func (p *thresholdProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	return p.ChatStream(ctx, req, func(string) {})
}
func (p *thresholdProvider) ChatStream(_ context.Context, req llm.ChatRequest, _ func(string)) (*llm.ChatResponse, error) {
	p.calls++
	n := llm.EstimateTokens(req)
	p.reqTokens = append(p.reqTokens, n)
	if n > p.window {
		return nil, fmt.Errorf("provider: %w (status 400)", llm.ErrContextOverflow)
	}
	return endResp("ok"), nil
}

// TestLoopHardGuardShrinksBelowGuardBeforeSend: three giant results are protected
// by the gentle proactive pass (keepRecent=3), so only the hard GuardAt guard can
// bring the prompt below the ceiling. Without it, an over-guard prompt is sent.
func TestLoopHardGuardShrinksBelowGuardBeforeSend(t *testing.T) {
	convo := []llm.Message{
		llm.TextMessage(llm.RoleUser, "go"),
		toolUseMsg("t1"), bigResultMsg("t1", 16000), // ~4000 tokens each
		toolUseMsg("t2"), bigResultMsg("t2", 16000),
		toolUseMsg("t3"), bigResultMsg("t3", 16000),
	}
	prov := &windowProvider{window: 8192} // GuardAt = 6963 tokens
	loop := Loop{Provider: prov}
	if _, _, err := loop.Run(context.Background(), convo, nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	guard := contextwindow.For(8192).GuardAt
	if prov.lastReqTokens > guard {
		t.Fatalf("sent %d tokens, want <= GuardAt %d (hard guard did not engage)", prov.lastReqTokens, guard)
	}
}

// TestLoopRecoversWhenOverflowIsGiantNewestResult: the single block causing the
// overflow is the NEWEST tool_result — exactly what keepRecent protects. Reactive
// recovery must clear even that (keepRecent=0) to make progress and recover,
// rather than re-send the same over-window prompt.
func TestLoopRecoversWhenOverflowIsGiantNewestResult(t *testing.T) {
	convo := []llm.Message{
		llm.TextMessage(llm.RoleUser, "read the big file"),
		toolUseMsg("t1"), bigResultMsg("t1", 40000), // ~10000 tokens, alone exceeds the 8192 window
	}
	prov := &thresholdProvider{window: 8192}
	loop := Loop{Provider: prov}
	_, final, err := loop.Run(context.Background(), convo, nil)
	if err != nil {
		t.Fatalf("did not recover from a giant newest result (session bricked): %v", err)
	}
	if final == nil || final.StopReason != llm.StopEndTurn {
		t.Fatalf("turn did not end cleanly: %+v", final)
	}
	if prov.calls < 2 {
		t.Fatalf("expected a retry after clearing the giant result, calls=%d", prov.calls)
	}
	if prov.reqTokens[len(prov.reqTokens)-1] > prov.window {
		t.Fatalf("final request %d still exceeds window %d", prov.reqTokens[len(prov.reqTokens)-1], prov.window)
	}
}

// TestLoopSkipsRetryWhenClearingCantHelp: a text-dominated overflow can't be
// reduced by clearing tool results. The harness must NOT waste a retry re-sending
// an identical over-window prompt; it surfaces the overflow (compaction, a later
// slice, is the lever). Exactly one provider call, error is ErrContextOverflow.
func TestLoopSkipsRetryWhenClearingCantHelp(t *testing.T) {
	convo := []llm.Message{llm.TextMessage(llm.RoleUser, strings.Repeat("word ", 40000))} // ~50k tokens of text, no results
	prov := &thresholdProvider{window: 8192}
	loop := Loop{Provider: prov}
	_, _, err := loop.Run(context.Background(), convo, nil)
	if !errors.Is(err, llm.ErrContextOverflow) {
		t.Fatalf("want ErrContextOverflow, got %v", err)
	}
	if prov.calls != 1 {
		t.Fatalf("expected exactly 1 call (no wasteful identical retry), got %d", prov.calls)
	}
}

// TestSupervisorCapsIterations: a model that never stops requesting tools is
// halted by the iteration cap (no infinite loop).

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
func (p *scriptedProvider) ChatStream(ctx context.Context, req llm.ChatRequest, onText func(string)) (*llm.ChatResponse, error) {
	r, err := p.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	if t := r.Text(); t != "" {
		onText(t)
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

// overflowThenProvider rejects the FIRST request as too long (as Anthropic does
// with a 400 "prompt is too long"), then succeeds — modelling the exact-count vs
// estimate mismatch that recovery must survive. It records each request's size.
type overflowThenProvider struct {
	calls     int
	reqTokens []int
	after     *llm.ChatResponse
}

func (p *overflowThenProvider) Name() string                                    { return "overflow-then" }
func (p *overflowThenProvider) Models(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (p *overflowThenProvider) Ping(context.Context) error                      { return nil }
func (p *overflowThenProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	return p.ChatStream(ctx, req, func(string) {})
}
func (p *overflowThenProvider) ChatStream(_ context.Context, req llm.ChatRequest, _ func(string)) (*llm.ChatResponse, error) {
	p.calls++
	p.reqTokens = append(p.reqTokens, llm.EstimateTokens(req))
	if p.calls == 1 {
		return nil, fmt.Errorf("provider: %w (status 400)", llm.ErrContextOverflow)
	}
	return p.after, nil
}

// TestLoopRecoversFromContextOverflow: when the provider rejects the prompt as
// too long, the loop shrinks (clears old tool-result bodies) and retries once,
// ending the turn cleanly instead of bricking the session. The retried request
// MUST be smaller — a blind retry of the identical prompt would fail again.
func TestLoopRecoversFromContextOverflow(t *testing.T) {
	bigResult := func(id string) llm.Message {
		return llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{
			Type: llm.BlockToolResult, ToolResult: &llm.ToolResult{ToolUseID: id, Content: strings.Repeat("x", 8000)},
		}}}
	}
	toolUse := func(id string) llm.Message {
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{
			Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: id, Name: "read_file", Input: json.RawMessage(`{}`)},
		}}}
	}
	convo := []llm.Message{
		llm.TextMessage(llm.RoleUser, "build a time service"),
		toolUse("t1"), bigResult("t1"), toolUse("t2"), bigResult("t2"),
		toolUse("t3"), bigResult("t3"), toolUse("t4"), bigResult("t4"),
	}
	prov := &overflowThenProvider{after: endResp("recovered")}
	loop := Loop{Provider: prov}

	_, final, err := loop.Run(context.Background(), convo, nil)
	if err != nil {
		t.Fatalf("loop did not recover from overflow (session would be bricked): %v", err)
	}
	if final == nil || final.StopReason != llm.StopEndTurn {
		t.Fatalf("turn did not end cleanly after recovery: %+v", final)
	}
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (overflow, then a shrunk retry)", prov.calls)
	}
	if !(prov.reqTokens[1] < prov.reqTokens[0]) {
		t.Fatalf("retry was not shrunk: first=%d retry=%d (blind retry would re-fail)", prov.reqTokens[0], prov.reqTokens[1])
	}
}

// TestLoopSkipsDoomedRetryWhenTextFloorExceedsWindow: when an irreducible text
// floor already exceeds the window, clearing the ALSO-present tool results makes
// partial progress but the prompt is still over-window. The harness must not fire
// a doomed retry that re-overflows — it retries only when clearing gets the prompt
// under a safe target. Exactly one provider call.
func TestLoopSkipsDoomedRetryWhenTextFloorExceedsWindow(t *testing.T) {
	convo := []llm.Message{
		llm.TextMessage(llm.RoleUser, strings.Repeat("word ", 20000)), // ~25k tokens of irreducible text > 8192
		toolUseMsg("t1"), bigResultMsg("t1", 20000), // ~5k tokens of clearable result
		toolUseMsg("t2"), bigResultMsg("t2", 20000),
	}
	prov := &thresholdProvider{window: 8192}
	loop := Loop{Provider: prov}
	_, _, err := loop.Run(context.Background(), convo, nil)
	if !errors.Is(err, llm.ErrContextOverflow) {
		t.Fatalf("want ErrContextOverflow surfaced, got %v", err)
	}
	if prov.calls != 1 {
		t.Fatalf("expected 1 call (no doomed retry — clearing can't get under window), got %d", prov.calls)
	}
}

// TestLoopRetriesWhenClearingFitsUnderGuard: after clearing, an irreducible text
// floor can land BELOW the window (and below GuardAt) even if it didn't reach the
// aggressive shrink target. That prompt fits — the harness must retry and recover,
// not surface an error. (Guards against over-correcting the doomed-retry fix.)
func TestLoopRetriesWhenClearingFitsUnderGuard(t *testing.T) {
	convo := []llm.Message{
		llm.TextMessage(llm.RoleUser, strings.Repeat("word ", 100000)), // ~125k tok text: fits a 200k window, but > est/2 target
		toolUseMsg("t1"), bigResultMsg("t1", 400000), // ~100k tok clearable result pushes the prompt over the window
	}
	prov := &thresholdProvider{window: 200000} // GuardAt = 170000
	loop := Loop{Provider: prov}
	_, final, err := loop.Run(context.Background(), convo, nil)
	if err != nil {
		t.Fatalf("did not recover a prompt that fits under the window after clearing: %v", err)
	}
	if final == nil || final.StopReason != llm.StopEndTurn {
		t.Fatalf("turn did not end cleanly: %+v", final)
	}
	if prov.calls != 2 {
		t.Fatalf("expected a retry once clearing brought the prompt under GuardAt, calls=%d", prov.calls)
	}
}

// TestLoopRequestsGenerousMaxTokens: the harness must request enough output room
// to write a full file — the 4096 default truncates a write_file tool input
// mid-JSON and fails generation deterministically.
func TestLoopRequestsGenerousMaxTokens(t *testing.T) {
	prov := &outputCappedProvider{maxOut: 64000} // a capable model (e.g. opus-4-8)
	loop := Loop{Provider: prov}
	if _, _, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "make a change")}, nil); err != nil {
		t.Fatal(err)
	}
	if prov.lastReq.MaxTokens < 30000 {
		t.Fatalf("harness requested max_tokens=%d — too small to write a full file (the 4096 default truncates write_file)", prov.lastReq.MaxTokens)
	}
}

// outputCappedProvider advertises a small max-OUTPUT cap (like Haiku 3's 4096) and
// records the last request — the harness must never request more output than this,
// or the provider rejects it with an unrecoverable 400.
type outputCappedProvider struct {
	maxOut  int
	lastReq llm.ChatRequest
}

func (p *outputCappedProvider) Name() string                                    { return "capped" }
func (p *outputCappedProvider) MaxOutputTokens() int                            { return p.maxOut }
func (p *outputCappedProvider) Models(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (p *outputCappedProvider) Ping(context.Context) error                      { return nil }
func (p *outputCappedProvider) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	p.lastReq = req
	return endResp("ok"), nil
}
func (p *outputCappedProvider) ChatStream(ctx context.Context, req llm.ChatRequest, _ func(string)) (*llm.ChatResponse, error) {
	return p.Chat(ctx, req)
}

// TestLoopCapsMaxTokensAtModelOutput: the requested max_tokens must never exceed
// the model's advertised output cap (or the provider 400s unrecoverably).
func TestLoopCapsMaxTokensAtModelOutput(t *testing.T) {
	prov := &outputCappedProvider{maxOut: 4096}
	loop := Loop{Provider: prov}
	if _, _, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "hi")}, nil); err != nil {
		t.Fatal(err)
	}
	if prov.lastReq.MaxTokens > 4096 {
		t.Fatalf("harness requested max_tokens=%d > the model's 4096 output cap (would 400)", prov.lastReq.MaxTokens)
	}
}

// TestClampMaxTokensFitsWindow: the requested output room is generous when the
// input is small, but clamped so input + output never exceeds the window (which
// would itself be a provider error).
func TestClampMaxTokensFitsWindow(t *testing.T) {
	if got := clampMaxTokens(32000, 200000, 5000); got != 32000 {
		t.Errorf("small input: got %d max_tokens, want the full 32000", got)
	}
	got := clampMaxTokens(32000, 200000, 190000)
	if 190000+got > 200000 {
		t.Errorf("input 190000 + max_tokens %d exceeds the 200000 window", got)
	}
	if got < 1024 {
		t.Errorf("clamp fell below the floor: %d", got)
	}
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
