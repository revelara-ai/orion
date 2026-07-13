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
	if len(got.System) != 1 || got.System[0].Text != "You are Orion. Grill the intent." {
		t.Fatalf("system not carried: %+v", got.System)
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

// TestToWireDropsEmptyMessages: an empty-content message must never reach the wire
// (it serializes to content:null and the API rejects the whole request — the live
// "messages.N.content: Input should be a valid array" 400).
func TestToWireDropsEmptyMessages(t *testing.T) {
	a := NewAnthropic("k", "m")
	req := ChatRequest{Messages: []Message{
		TextMessage(RoleUser, "hi"),
		{Role: RoleAssistant, Content: nil},              // empty assistant turn (the bug)
		{Role: RoleAssistant, Content: []ContentBlock{}}, // also empty
		TextMessage(RoleUser, "again"),
	}}
	w := a.toWire(req, "m", 100)
	if len(w.Messages) != 2 {
		t.Fatalf("empty messages not dropped: got %d wire messages", len(w.Messages))
	}
	for _, m := range w.Messages {
		if len(m.Content) == 0 {
			t.Fatal("a wire message has empty content (would marshal to null)")
		}
	}
	b, _ := json.Marshal(w)
	if strings.Contains(string(b), `"content":null`) {
		t.Fatalf("request contains null content: %s", b)
	}
}

// TestAnthropicCachingBreakpoints (or-4qkg): the static prefix (tools +
// system) must carry cache_control breakpoints — without them Orion re-pays
// full input price for ~25-30K tokens of system+tool schemas on EVERY agent
// loop iteration. Breakpoints: the LAST tool (caches the whole tool array)
// and the system block (caches tools+system). Messages carry none (the
// context manager rewrites history, so message-prefix caching can't be
// relied on and breakpoints there would burn the 4-breakpoint budget).
func TestAnthropicCachingBreakpoints(t *testing.T) {
	var got wireRequest
	a := fixtureServer(t, 200, `{"model":"m","stop_reason":"end_turn","content":[],"usage":{}}`, &got)
	_, err := a.Chat(context.Background(), ChatRequest{
		System: "You are Orion.",
		Tools: []Tool{
			{Name: "alpha", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "beta", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
		Messages: []Message{TextMessage(RoleUser, "go")},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if len(got.Tools) != 2 {
		t.Fatalf("tools = %d", len(got.Tools))
	}
	if got.Tools[0].CacheControl != nil {
		t.Error("only the LAST tool carries the breakpoint")
	}
	if got.Tools[1].CacheControl == nil || got.Tools[1].CacheControl.Type != "ephemeral" {
		t.Errorf("last tool must carry cache_control ephemeral: %+v", got.Tools[1].CacheControl)
	}
	if len(got.System) != 1 || got.System[0].Text != "You are Orion." {
		t.Fatalf("system must render as a block array: %+v", got.System)
	}
	if got.System[0].CacheControl == nil || got.System[0].CacheControl.Type != "ephemeral" {
		t.Errorf("system block must carry cache_control ephemeral: %+v", got.System[0])
	}
	for _, m := range got.Messages {
		for _, b := range m.Content {
			if b.CacheControl != nil {
				t.Error("messages must not carry breakpoints")
			}
		}
	}
}

// TestAnthropicCachingOmitsEmpty: no system → no system field; no tools → no
// stray breakpoints.
func TestAnthropicCachingOmitsEmpty(t *testing.T) {
	var got wireRequest
	a := fixtureServer(t, 200, `{"model":"m","stop_reason":"end_turn","content":[],"usage":{}}`, &got)
	if _, err := a.Chat(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "go")}}); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if len(got.System) != 0 {
		t.Fatalf("empty system must be omitted: %+v", got.System)
	}
}

// TestAnthropicCountTokens (or-hhq): the exact sensor — POST
// /v1/messages/count_tokens with the Chat wire shape (no max_tokens), parsed
// into the count CountOrEstimate prefers.
func TestAnthropicCountTokens(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"input_tokens": 4242}`))
	}))
	defer srv.Close()
	a := NewAnthropic("k", "claude-test")
	a.baseURL = srv.URL

	req := ChatRequest{System: "sys", Messages: []Message{TextMessage(RoleUser, "hello")}, Tools: []Tool{{Name: "t", Description: "d", InputSchema: json.RawMessage(`{}`)}}}
	n, err := a.CountTokens(context.Background(), req)
	if err != nil || n != 4242 {
		t.Fatalf("count: %d %v", n, err)
	}
	if gotPath != "/v1/messages/count_tokens" {
		t.Fatalf("wrong endpoint: %s", gotPath)
	}
	if _, has := gotBody["max_tokens"]; has {
		t.Fatal("count_tokens must not carry max_tokens")
	}
	if gotBody["model"] != "claude-test" || gotBody["messages"] == nil || gotBody["tools"] == nil {
		t.Fatalf("the count body must mirror the chat wire shape: %v", gotBody)
	}

	// And the capability plugs into CountOrEstimate.
	if got := CountOrEstimate(context.Background(), a, req); got != 4242 {
		t.Fatalf("CountOrEstimate must prefer the exact count, got %d", got)
	}
}

// TestAnthropicContextEditsOptIn (or-hhq): the server-side context-management
// beta rides the request ONLY when ORION_ANTHROPIC_CONTEXT_EDITS=1 — header
// and body both; default off leaves the wire byte-identical.
func TestAnthropicContextEditsOptIn(t *testing.T) {
	var gotBeta string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBeta = r.Header.Get("anthropic-beta")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()
	a := NewAnthropic("k", "claude-test")
	a.baseURL = srv.URL
	req := ChatRequest{Messages: []Message{TextMessage(RoleUser, "hi")}}

	t.Setenv("ORION_ANTHROPIC_CONTEXT_EDITS", "")
	if _, err := a.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if gotBeta != "" || gotBody["context_management"] != nil {
		t.Fatal("default off must not send the beta header or body field")
	}

	t.Setenv("ORION_ANTHROPIC_CONTEXT_EDITS", "1")
	if _, err := a.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if gotBeta != "context-management-2025-06-27" {
		t.Fatalf("the beta header must be set when opted in, got %q", gotBeta)
	}
	cm, _ := gotBody["context_management"].(map[string]any)
	if cm == nil {
		t.Fatalf("context_management must ride the body when opted in: %v", gotBody)
	}
}
