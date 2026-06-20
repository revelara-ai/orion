package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fixtureServer returns an httptest server that replies with the given JSON +
// status, and captures the last request body for assertions.
func fixtureServer(t *testing.T, status int, body string, captured *wireRequest) *Anthropic {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if captured != nil {
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, captured)
		}
		w.Header().Set("content-type", "application/json")
		if status == 429 || status == 529 {
			w.Header().Set("retry-after", "1")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	a := NewAnthropic("test-key", "claude-opus-4-8")
	a.baseURL = srv.URL
	return a
}

// TestAnthropicChatParsesTextAndUsage: a normal text response maps to canonical
// content + stop reason + usage.
func TestAnthropicChatParsesTextAndUsage(t *testing.T) {
	a := fixtureServer(t, 200, `{
		"model":"claude-opus-4-8","stop_reason":"end_turn",
		"content":[{"type":"text","text":"Which port should it listen on?"}],
		"usage":{"input_tokens":12,"output_tokens":8,"cache_read_input_tokens":100}
	}`, nil)
	res, err := a.Chat(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "build a time service")}})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if res.StopReason != StopEndTurn {
		t.Fatalf("stop = %q, want end_turn", res.StopReason)
	}
	if res.Text() != "Which port should it listen on?" {
		t.Fatalf("text = %q", res.Text())
	}
	if res.Usage.InputTokens != 12 || res.Usage.OutputTokens != 8 || res.Usage.CacheReadInputTokens != 100 {
		t.Fatalf("usage = %+v", res.Usage)
	}
}

// TestAnthropicChatParsesToolUse: a tool_use response surfaces a ToolUse the loop
// can dispatch.
func TestAnthropicChatParsesToolUse(t *testing.T) {
	a := fixtureServer(t, 200, `{
		"model":"claude-opus-4-8","stop_reason":"tool_use",
		"content":[
			{"type":"text","text":"Let me check what's still open."},
			{"type":"tool_use","id":"tu_1","name":"check_completeness","input":{"intent":"time service"}}
		],
		"usage":{"input_tokens":20,"output_tokens":15}
	}`, nil)
	res, err := a.Chat(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "x")}})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if res.StopReason != StopToolUse {
		t.Fatalf("stop = %q, want tool_use", res.StopReason)
	}
	tus := res.ToolUses()
	if len(tus) != 1 || tus[0].Name != "check_completeness" || tus[0].ID != "tu_1" {
		t.Fatalf("tool uses = %+v", tus)
	}
	if !strings.Contains(string(tus[0].Input), "time service") {
		t.Fatalf("tool input not preserved: %s", tus[0].Input)
	}
}

// TestAnthropicChatHandlesRefusal: a refusal (200 + stop_reason refusal, empty
// content) is normalized — the loop branches on stop BEFORE reading content.
func TestAnthropicChatHandlesRefusal(t *testing.T) {
	a := fixtureServer(t, 200, `{"model":"m","stop_reason":"refusal","content":[],"usage":{}}`, nil)
	res, err := a.Chat(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "x")}})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if res.StopReason != StopRefusal {
		t.Fatalf("stop = %q, want refusal", res.StopReason)
	}
}

// TestAnthropicRequestShape: system is carried separately, messages + tools map
// to the wire correctly (incl. a tool_result round-trip).
func TestAnthropicRequestShape(t *testing.T) {
	var got wireRequest
	a := fixtureServer(t, 200, `{"model":"m","stop_reason":"end_turn","content":[],"usage":{}}`, &got)
	_, err := a.Chat(context.Background(), ChatRequest{
		System: "You are Orion. Grill the intent.",
		Tools:  []Tool{{Name: "check_completeness", Description: "what's open", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Messages: []Message{
			TextMessage(RoleUser, "build a time service"),
			{Role: RoleAssistant, Content: []ContentBlock{{Type: BlockToolUse, ToolUse: &ToolUse{ID: "tu_1", Name: "check_completeness", Input: json.RawMessage(`{}`)}}}},
			{Role: RoleUser, Content: []ContentBlock{{Type: BlockToolResult, ToolResult: &ToolResult{ToolUseID: "tu_1", Content: "port unanswered"}}}},
		},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if got.System != "You are Orion. Grill the intent." {
		t.Fatalf("system not carried: %q", got.System)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "check_completeness" {
		t.Fatalf("tools not mapped: %+v", got.Tools)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(got.Messages))
	}
	last := got.Messages[2].Content[0]
	if last.Type != "tool_result" || last.ToolUseID != "tu_1" {
		t.Fatalf("tool_result not mapped: %+v", last)
	}
}

// TestAnthropicRetryClassification: 529 → RetryAfter (retried, then succeeds);
// 400 → a non-retryable error surfaced to the caller.
func TestAnthropicRetryClassification(t *testing.T) {
	// 400 is non-retryable.
	a := fixtureServer(t, 400, `{"type":"error","error":{"message":"bad request"}}`, nil)
	if _, err := a.Chat(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "x")}}); err == nil {
		t.Fatal("400 should surface a non-retryable error")
	} else if !strings.Contains(err.Error(), "400") {
		t.Fatalf("error should mention status: %v", err)
	}
}
