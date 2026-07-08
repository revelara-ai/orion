package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func geminiTestServer(t *testing.T, respJSON string, captured *gemRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.Write([]byte(`{"models":[{"name":"models/gemini-2.5-pro"},{"name":"models/gemini-2.5-flash"}]}`))
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

func TestGeminiChatTranslation(t *testing.T) {
	var got gemRequest
	srv := geminiTestServer(t, `{
		"modelVersion":"gemini-2.5-pro",
		"candidates":[{"content":{"role":"model","parts":[
			{"text":"checking"},
			{"functionCall":{"name":"read_file","args":{"path":"a.go"}}}
		]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":6,"cachedContentTokenCount":2}
	}`, &got)
	defer srv.Close()

	g := NewGemini(GeminiConfig{APIKey: "k", Model: "gemini-2.5-pro", BaseURL: srv.URL})
	req := ChatRequest{
		System: "be brief",
		Messages: []Message{
			TextMessage(RoleUser, "hello"),
			{Role: RoleAssistant, Content: []ContentBlock{
				{Type: BlockToolUse, ToolUse: &ToolUse{ID: "call_ls_1", Name: "ls", Input: json.RawMessage(`{"d":"."}`)}},
			}},
			{Role: RoleUser, Content: []ContentBlock{
				{Type: BlockToolResult, ToolResult: &ToolResult{ToolUseID: "call_ls_1", Content: "a.go"}},
			}},
		},
		Tools:     []Tool{{Name: "ls", Description: "list", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		MaxTokens: 100,
	}
	resp, err := g.Chat(context.Background(), req)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// Request wire shape.
	if got.SystemInstruction == nil || got.SystemInstruction.Parts[0].Text != "be brief" {
		t.Errorf("systemInstruction wrong: %+v", got.SystemInstruction)
	}
	if got.Contents[0].Role != "user" || got.Contents[0].Parts[0].Text != "hello" {
		t.Errorf("user content wrong: %+v", got.Contents[0])
	}
	fc := got.Contents[1]
	if fc.Role != "model" || fc.Parts[0].FunctionCall == nil || fc.Parts[0].FunctionCall.Name != "ls" {
		t.Errorf("functionCall content wrong: %+v", fc)
	}
	fr := got.Contents[2]
	// functionResponse must carry the function NAME, recovered from the
	// tool_use with the matching synthesized id.
	if fr.Role != "user" || fr.Parts[0].FunctionResponse == nil || fr.Parts[0].FunctionResponse.Name != "ls" {
		t.Errorf("functionResponse content wrong: %+v", fr)
	}
	if fr.Parts[0].FunctionResponse.Response["result"] != "a.go" {
		t.Errorf("functionResponse payload wrong: %+v", fr.Parts[0].FunctionResponse.Response)
	}
	if len(got.Tools) != 1 || got.Tools[0].FunctionDeclarations[0].Name != "ls" {
		t.Errorf("tools wrong: %+v", got.Tools)
	}
	if got.GenerationConfig == nil || got.GenerationConfig.MaxOutputTokens != 100 {
		t.Errorf("generationConfig wrong: %+v", got.GenerationConfig)
	}

	// Response mapping: STOP + functionCall part → tool_use, synthesized id.
	if resp.StopReason != StopToolUse {
		t.Errorf("stop = %q, want tool_use", resp.StopReason)
	}
	tus := resp.ToolUses()
	if len(tus) != 1 || tus[0].Name != "read_file" || tus[0].ID != "call_read_file_1" {
		t.Fatalf("tool uses wrong: %+v", tus)
	}
	var args map[string]string
	if err := json.Unmarshal(tus[0].Input, &args); err != nil || args["path"] != "a.go" {
		t.Errorf("args wrong: %s (%v)", tus[0].Input, err)
	}
	if resp.Text() != "checking" {
		t.Errorf("text = %q", resp.Text())
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 6 || resp.Usage.CacheReadInputTokens != 2 {
		t.Errorf("usage wrong: %+v", resp.Usage)
	}
}

func TestGeminiErrorToolResult(t *testing.T) {
	var got gemRequest
	srv := geminiTestServer(t, `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`, &got)
	defer srv.Close()
	g := NewGemini(GeminiConfig{APIKey: "k", Model: "m", BaseURL: srv.URL})
	_, err := g.Chat(context.Background(), ChatRequest{Messages: []Message{
		{Role: RoleAssistant, Content: []ContentBlock{{Type: BlockToolUse, ToolUse: &ToolUse{ID: "c1", Name: "run", Input: json.RawMessage(`{}`)}}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: BlockToolResult, ToolResult: &ToolResult{ToolUseID: "c1", Content: "boom", IsError: true}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	frp := got.Contents[1].Parts[0].FunctionResponse
	if frp.Response["error"] != "boom" {
		t.Errorf("error result not mapped to error key: %+v", frp.Response)
	}
}

func TestGeminiStopMapping(t *testing.T) {
	cases := []struct {
		finish  string
		hasCall bool
		want    StopReason
	}{
		{"STOP", false, StopEndTurn},
		{"STOP", true, StopToolUse},
		{"MAX_TOKENS", false, StopMaxTokens},
		{"SAFETY", false, StopRefusal},
		{"PROHIBITED_CONTENT", false, StopRefusal},
		{"RECITATION", false, StopRefusal},
		{"OTHER", false, StopUnknown},
	}
	for _, c := range cases {
		if got := gemStop(c.finish, c.hasCall); got != c.want {
			t.Errorf("gemStop(%q,%v) = %q, want %q", c.finish, c.hasCall, got, c.want)
		}
	}
}

func TestGeminiModelsPingCapabilities(t *testing.T) {
	srv := geminiTestServer(t, `{}`, nil)
	defer srv.Close()
	g := NewGemini(GeminiConfig{APIKey: "k", Model: "gemini-2.5-pro", BaseURL: srv.URL})
	ms, err := g.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].ID != "gemini-2.5-pro" || !ms[0].Tools {
		t.Errorf("models wrong (name prefix must be stripped, Tools true): %+v", ms)
	}
	if err := g.Ping(context.Background()); err != nil {
		t.Errorf("ping with key: %v", err)
	}
	noKey := NewGemini(GeminiConfig{Model: "m", BaseURL: srv.URL})
	if err := noKey.Ping(context.Background()); err == nil {
		t.Error("ping without key must fail")
	}
	if g.ContextWindow() != 1_000_000 {
		t.Errorf("gemini-2.5 window = %d, want 1M", g.ContextWindow())
	}
	override := NewGemini(GeminiConfig{APIKey: "k", Model: "gemini-2.5-pro", BaseURL: srv.URL, ContextWindow: 32768, MaxOutput: 2048})
	if override.ContextWindow() != 32768 || override.MaxOutputTokens() != 2048 {
		t.Errorf("config overrides not honored: %d %d", override.ContextWindow(), override.MaxOutputTokens())
	}
	var _ Provider = g
}
