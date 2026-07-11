package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestEstimateTokensApproximatesQuarterOfContent: the estimator is a cheap
// provider-agnostic gauge (~chars/4). It must count system + every block's
// payload (text, tool_use input, tool_result content), not just top-level text.
func TestEstimateTokensApproximatesQuarterOfContent(t *testing.T) {
	body := strings.Repeat("x", 4000) // ~1000 tokens of content
	req := ChatRequest{
		System: strings.Repeat("s", 400), // ~100 tokens
		Messages: []Message{
			TextMessage(RoleUser, body),
			{Role: RoleUser, Content: []ContentBlock{{
				Type:       BlockToolResult,
				ToolResult: &ToolResult{ToolUseID: "t1", Content: strings.Repeat("y", 800)}, // ~200 tokens
			}}},
		},
	}
	got := EstimateTokens(req)
	// System(100) + text(1000) + tool_result(200) ≈ 1300, allow generous slack for
	// per-message/structural overhead but reject wildly-off estimates.
	if got < 1200 || got > 1600 {
		t.Fatalf("EstimateTokens = %d, want ~1300 (1200..1600)", got)
	}
}

// TestEstimateTokensMonotonic: appending content never shrinks the estimate.
func TestEstimateTokensMonotonic(t *testing.T) {
	base := ChatRequest{Messages: []Message{TextMessage(RoleUser, "hello")}}
	bigger := ChatRequest{Messages: append(append([]Message(nil), base.Messages...),
		TextMessage(RoleAssistant, strings.Repeat("more content ", 500)))}
	if EstimateTokens(bigger) <= EstimateTokens(base) {
		t.Fatalf("estimate did not grow: base=%d bigger=%d", EstimateTokens(base), EstimateTokens(bigger))
	}
}

// stubProvider satisfies Provider with no-ops and, deliberately, does NOT
// implement TokenCounter or ContextWindow — modelling a local/offline brain.
type stubProvider struct{}

func (stubProvider) Name() string { return "stub" }
func (stubProvider) Chat(context.Context, ChatRequest) (*ChatResponse, error) {
	return &ChatResponse{}, nil
}
func (stubProvider) ChatStream(context.Context, ChatRequest, func(string)) (*ChatResponse, error) {
	return &ChatResponse{}, nil
}
func (stubProvider) Models(context.Context) ([]ModelInfo, error) { return nil, nil }
func (stubProvider) Ping(context.Context) error                  { return nil }

// countingProvider additionally implements TokenCounter with an exact count.
type countingProvider struct {
	stubProvider
	count int
}

func (c countingProvider) CountTokens(context.Context, ChatRequest) (int, error) {
	return c.count, nil
}

// TestCountOrEstimateFallsBackWhenProviderCannotCount: a provider without
// TokenCounter must degrade to EstimateTokens rather than fail.
func TestCountOrEstimateFallsBackWhenProviderCannotCount(t *testing.T) {
	req := ChatRequest{Messages: []Message{TextMessage(RoleUser, strings.Repeat("z", 4000))}}
	got := CountOrEstimate(context.Background(), stubProvider{}, req)
	if got != EstimateTokens(req) {
		t.Fatalf("CountOrEstimate = %d, want fallback to EstimateTokens = %d", got, EstimateTokens(req))
	}
}

// TestCountOrEstimatePrefersExactCounter: when the provider CAN count, its exact
// number is authoritative over the heuristic.
func TestCountOrEstimatePrefersExactCounter(t *testing.T) {
	req := ChatRequest{Messages: []Message{TextMessage(RoleUser, "tiny")}}
	got := CountOrEstimate(context.Background(), countingProvider{count: 987654}, req)
	if got != 987654 {
		t.Fatalf("CountOrEstimate = %d, want exact counter value 987654", got)
	}
}

// TestAnthropicMapsPromptTooLongToOverflow: the production 400 ("prompt is too
// long: N tokens > M maximum") must surface as ErrContextOverflow so the harness
// can branch on it and recover instead of bricking the session.
func TestAnthropicMapsPromptTooLongToOverflow(t *testing.T) {
	a := fixtureServer(t, 400, `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 1009821 tokens > 1000000 maximum"}}`, nil)
	_, err := a.Chat(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "hi")}})
	if err == nil {
		t.Fatal("expected an error for a 400 response")
	}
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("error %q is not ErrContextOverflow", err)
	}
}

// TestAnthropicOtherBadRequestIsNotOverflow: an unrelated 400 must remain a plain
// error, NOT ErrContextOverflow (we only recover from genuine overflow).
func TestAnthropicOtherBadRequestIsNotOverflow(t *testing.T) {
	a := fixtureServer(t, 400, `{"type":"error","error":{"type":"invalid_request_error","message":"model: unknown model 'foo'"}}`, nil)
	_, err := a.Chat(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "hi")}})
	if err == nil {
		t.Fatal("expected an error for a 400 response")
	}
	if errors.Is(err, ErrContextOverflow) {
		t.Fatalf("unrelated 400 wrongly classified as ErrContextOverflow: %v", err)
	}
}

// TestAnthropicReportsContextWindow: the Anthropic brain advertises its window so
// the policy governs it precisely rather than via the conservative fallback.
func TestAnthropicReportsContextWindow(t *testing.T) {
	var prov Provider = NewAnthropic("k", "claude-opus-4-8")
	cw, ok := prov.(ContextWindow)
	if !ok {
		t.Fatal("Anthropic does not implement ContextWindow")
	}
	if cw.ContextWindow() != 1_000_000 {
		t.Fatalf("ContextWindow = %d, want 1000000", cw.ContextWindow())
	}
}

// TestAnthropicContextWindowIsPerModel: a smaller-window model (selectable at
// runtime via /model) must report ITS window, not a blanket 1M — otherwise the
// policy thresholds sit above the real ceiling and proactive clearing never fires.
func TestAnthropicContextWindowIsPerModel(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  int
	}{
		// 1M tier (whitelist): Opus 4.6+, Sonnet 4.6, Sonnet 5, Fable 5, Mythos 5.
		{"claude-opus-4-8", 1_000_000},
		{"claude-opus-4-7", 1_000_000},
		{"claude-opus-4-6", 1_000_000},
		{"claude-sonnet-4-6", 1_000_000},
		{"claude-sonnet-5", 1_000_000},
		{"claude-fable-5", 1_000_000},
		{"claude-mythos-5", 1_000_000},
		// 200K: Haiku, and every LEGACY model — including legacy Opus, which must NOT
		// be treated as 1M just because the id contains "opus".
		{"claude-haiku-4-5", 200_000},
		{"claude-sonnet-4-5", 200_000},
		{"claude-opus-4-5", 200_000},
		{"claude-opus-4-1", 200_000},
		{"some-unknown-model", 200_000}, // conservative default for an unrecognized id
	} {
		if got := NewAnthropic("k", tc.model).ContextWindow(); got != tc.want {
			t.Errorf("ContextWindow(%q) = %d, want %d", tc.model, got, tc.want)
		}
	}
}

// TestEstimateTokensCountsToolSpecs: tool specs (name+description+schema) are
// serialized into every request and counted by the provider, so the gauge MUST
// include them — otherwise a small local window is silently blown by the tool
// footprint alone while the estimate reads far under the clear threshold.
func TestEstimateTokensCountsToolSpecs(t *testing.T) {
	base := ChatRequest{Messages: []Message{TextMessage(RoleUser, "hi")}}
	withTools := base
	withTools.Tools = []Tool{{
		Name:        "read_file",
		Description: strings.Repeat("d", 400),
		InputSchema: json.RawMessage(strings.Repeat("x", 400)),
	}}
	if EstimateTokens(withTools) <= EstimateTokens(base)+150 {
		t.Fatalf("tool specs undercounted: base=%d withTools=%d (want +~200)", EstimateTokens(base), EstimateTokens(withTools))
	}
}

// TestAnthropicStreamMapsPromptTooLongToOverflow: the harness drives ChatStream
// exclusively, so the STREAMING path — not just Chat — must map the production
// 400 "prompt is too long" to ErrContextOverflow, or reactive recovery is dead
// code and the 1M brick is not actually fixed.
func TestAnthropicStreamMapsPromptTooLongToOverflow(t *testing.T) {
	a := fixtureServer(t, 400, `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 1009821 tokens > 1000000 maximum"}}`, nil)
	_, err := a.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "hi")}}, nil)
	if err == nil {
		t.Fatal("expected an error for a streaming 400")
	}
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("streaming 400 %q is not ErrContextOverflow — reactive recovery would be unreachable", err)
	}
}

// TestAnthropicStreamOtherBadRequestNotOverflow: an unrelated streaming 400 stays
// a plain error (we only shrink-and-retry genuine overflows).
func TestAnthropicStreamOtherBadRequestNotOverflow(t *testing.T) {
	a := fixtureServer(t, 400, `{"type":"error","error":{"type":"invalid_request_error","message":"model: unknown model 'foo'"}}`, nil)
	_, err := a.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "hi")}}, nil)
	if err == nil {
		t.Fatal("expected an error for a streaming 400")
	}
	if errors.Is(err, ErrContextOverflow) {
		t.Fatalf("unrelated streaming 400 wrongly classified as overflow: %v", err)
	}
}

// TestAnthropicStreamMaxTokensTruncationIsClear: a tool_use input cut off by
// max_tokens must be reported as a max_tokens/output-limit problem (not a generic
// "stream truncated"), so the failure isn't misread as transient provider infra.
func TestAnthropicStreamMaxTokensTruncationIsClear(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"type":"message_start","message":{"model":"m","usage":{"input_tokens":10}}}`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"write_file"}}`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.go\",\"content\":\"pack"}}`,
		`data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"}}`,
		`data: {"type":"message_stop"}`,
		"",
	}, "\n\n")
	a := fixtureServer(t, 200, sse, nil)
	_, err := a.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "write a file")}}, nil)
	if err == nil {
		t.Fatal("expected an error for a max_tokens-truncated tool_use input")
	}
	if !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("max_tokens truncation not clearly reported (would be misread as infra): %v", err)
	}
}

// TestAnthropicStreamMapsInStreamOverflowError: if overflow arrives as an in-stream
// SSE error event (HTTP 200 + event:error) before any text is emitted, it too must
// surface as ErrContextOverflow so recovery can fire.
func TestAnthropicStreamMapsInStreamOverflowError(t *testing.T) {
	sse := "event: error\n" +
		`data: {"type":"error","error":{"message":"prompt is too long: 1009821 tokens > 1000000 maximum"}}` + "\n\n"
	a := fixtureServer(t, 200, sse, nil)
	_, err := a.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "hi")}}, nil)
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("in-stream overflow error %v is not ErrContextOverflow", err)
	}
}
