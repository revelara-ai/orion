package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// openAITestServer returns a server that captures the request body and replies
// with the canned response JSON.
func openAITestServer(t *testing.T, respJSON string, captured *oaRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`{"data":[{"id":"qwen3-32b"},{"id":"llama-3.3-70b"}]}`))
			return
		}
		if captured != nil {
			if err := json.NewDecoder(r.Body).Decode(captured); err != nil {
				t.Errorf("decode request: %v", err)
			}
		}
		w.Write([]byte(respJSON))
	}))
}

func TestOpenAIChatTranslation(t *testing.T) {
	var got oaRequest
	srv := openAITestServer(t, `{
		"model":"qwen3-32b",
		"choices":[{"message":{"content":"hi there","tool_calls":[
			{"id":"call_abc","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a.go\"}"}}
		]},"finish_reason":"tool_calls"}],
		"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":3}}
	}`, &got)
	defer srv.Close()

	o := NewOpenAI(OpenAIConfig{Name: "lmstudio", BaseURL: srv.URL + "/v1", Model: "qwen3-32b"})
	req := ChatRequest{
		System: "be brief",
		Messages: []Message{
			TextMessage(RoleUser, "hello"),
			{Role: RoleAssistant, Content: []ContentBlock{
				{Type: BlockText, Text: "checking"},
				{Type: BlockToolUse, ToolUse: &ToolUse{ID: "call_1", Name: "ls", Input: json.RawMessage(`{}`)}},
			}},
			{Role: RoleUser, Content: []ContentBlock{
				{Type: BlockToolResult, ToolResult: &ToolResult{ToolUseID: "call_1", Content: "a.go", IsError: false}},
			}},
		},
		Tools:     []Tool{{Name: "ls", Description: "list", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		MaxTokens: 100,
	}
	resp, err := o.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// Request wire shape.
	if got.Messages[0].Role != "system" || got.Messages[0].Content != "be brief" {
		t.Errorf("system message wrong: %+v", got.Messages[0])
	}
	if got.Messages[1].Role != "user" || got.Messages[1].Content != "hello" {
		t.Errorf("user message wrong: %+v", got.Messages[1])
	}
	am := got.Messages[2]
	if am.Role != "assistant" || am.Content != "checking" || len(am.ToolCalls) != 1 || am.ToolCalls[0].ID != "call_1" || am.ToolCalls[0].Function.Name != "ls" {
		t.Errorf("assistant message wrong: %+v", am)
	}
	tm := got.Messages[3]
	if tm.Role != "tool" || tm.ToolCallID != "call_1" || tm.Content != "a.go" {
		t.Errorf("tool message wrong: %+v", tm)
	}
	if len(got.Tools) != 1 || got.Tools[0].Type != "function" || got.Tools[0].Function.Name != "ls" {
		t.Errorf("tools wrong: %+v", got.Tools)
	}
	if got.MaxTokens != 100 || got.Model != "qwen3-32b" {
		t.Errorf("model/max_tokens wrong: %s %d", got.Model, got.MaxTokens)
	}

	// Response mapping.
	if resp.StopReason != StopToolUse {
		t.Errorf("stop reason = %q, want tool_use", resp.StopReason)
	}
	if resp.Text() != "hi there" {
		t.Errorf("text = %q", resp.Text())
	}
	tus := resp.ToolUses()
	if len(tus) != 1 || tus[0].ID != "call_abc" || tus[0].Name != "read_file" {
		t.Errorf("tool uses wrong: %+v", tus)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 || resp.Usage.CacheReadInputTokens != 3 {
		t.Errorf("usage wrong: %+v", resp.Usage)
	}
}

func TestOpenAIErrorToolResultPrefixed(t *testing.T) {
	var got oaRequest
	srv := openAITestServer(t, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`, &got)
	defer srv.Close()
	o := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "m"})
	_, err := o.Chat(context.Background(), ChatRequest{Messages: []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: BlockToolResult, ToolResult: &ToolResult{ToolUseID: "c1", Content: "boom", IsError: true}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Messages[0].Content != "ERROR: boom" {
		t.Errorf("error tool result not prefixed: %q", got.Messages[0].Content)
	}
}

func TestOpenAIStopReasonMapping(t *testing.T) {
	cases := []struct {
		finish   string
		hasCalls bool
		want     StopReason
	}{
		{"stop", false, StopEndTurn},
		{"stop", true, StopToolUse}, // some local servers report "stop" even with tool_calls
		{"tool_calls", true, StopToolUse},
		{"length", false, StopMaxTokens},
		{"content_filter", false, StopRefusal},
		{"weird", false, StopUnknown},
	}
	for _, c := range cases {
		if got := oaStop(c.finish, c.hasCalls); got != c.want {
			t.Errorf("oaStop(%q,%v) = %q, want %q", c.finish, c.hasCalls, got, c.want)
		}
	}
}

func TestOpenAISynthesizesMissingToolCallID(t *testing.T) {
	srv := openAITestServer(t, `{"choices":[{"message":{"tool_calls":[
		{"type":"function","function":{"name":"ls","arguments":""}}
	]},"finish_reason":"tool_calls"}]}`, nil)
	defer srv.Close()
	o := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "m"})
	resp, err := o.Chat(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "x")}})
	if err != nil {
		t.Fatal(err)
	}
	tus := resp.ToolUses()
	if len(tus) != 1 || tus[0].ID == "" {
		t.Fatalf("missing id not synthesized: %+v", tus)
	}
	if string(tus[0].Input) != "{}" {
		t.Errorf("empty arguments not defaulted to {}: %q", tus[0].Input)
	}
}

func TestOpenAIModelsAndPing(t *testing.T) {
	srv := openAITestServer(t, `{}`, nil)
	defer srv.Close()
	o := NewOpenAI(OpenAIConfig{Name: "lmstudio", BaseURL: srv.URL + "/v1"})
	ms, err := o.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].ID != "qwen3-32b" || ms[0].Tools {
		t.Errorf("models wrong (Tools must be false — probe is the authority): %+v", ms)
	}
	if err := o.Ping(context.Background()); err != nil {
		t.Errorf("ping: %v", err)
	}
	srv.Close()
	if err := o.Ping(context.Background()); err == nil {
		t.Error("ping against closed server should fail")
	}
}

func TestOpenAICapabilities(t *testing.T) {
	o := NewOpenAI(OpenAIConfig{BaseURL: "http://x/v1", ContextWindow: 32768, MaxOutput: 4096})
	if o.ContextWindow() != 32768 || o.MaxOutputTokens() != 4096 {
		t.Errorf("capabilities not plumbed: %d %d", o.ContextWindow(), o.MaxOutputTokens())
	}
	var _ Provider = o // compile-time interface check (ChatStream added in Task 3 — stub until then)
}
