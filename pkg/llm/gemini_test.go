package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestGeminiOmitsEmptyToolSchemas(t *testing.T) {
	// Gemini rejects OBJECT schemas with no/empty properties; for no-arg tools
	// the declaration must omit parameters entirely.
	var got gemRequest
	srv := geminiTestServer(t, `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`, &got)
	defer srv.Close()
	g := NewGemini(GeminiConfig{APIKey: "k", Model: "m", BaseURL: srv.URL})
	_, err := g.Chat(context.Background(), ChatRequest{
		Messages: []Message{TextMessage(RoleUser, "x")},
		Tools: []Tool{
			{Name: "noargs", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "emptyprops", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
			{Name: "nilschema", InputSchema: nil},
			{Name: "full", InputSchema: json.RawMessage(`{"type":"object","properties":{"p":{"type":"string"}}}`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	decls := got.Tools[0].FunctionDeclarations
	for i, want := range []string{"noargs", "emptyprops", "nilschema"} {
		if decls[i].Name != want {
			t.Fatalf("tool order changed: %+v", decls)
		}
		if len(decls[i].Parameters) != 0 {
			t.Errorf("tool %s: parameters must be omitted, got %s", want, decls[i].Parameters)
		}
	}
	if len(decls[3].Parameters) == 0 {
		t.Errorf("full schema must be passed through, got empty")
	}
}

// TestGeminiSanitizesToolSchemas: Gemini function declarations accept an
// OpenAPI subset, not full JSON Schema — an additionalProperties ANYWHERE in
// any tool's parameters 400s the whole request (live failure: Orion's verify
// tool's env map at declarations[47]). The adapter must strip the rejected
// keywords recursively while leaving semantics-bearing structure intact.
func TestGeminiSanitizesToolSchemas(t *testing.T) {
	var got gemRequest
	srv := geminiTestServer(t, `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`, &got)
	defer srv.Close()
	g := NewGemini(GeminiConfig{APIKey: "k", Model: "m", BaseURL: srv.URL})
	schema := `{
		"type":"object",
		"$schema":"http://json-schema.org/draft-07/schema#",
		"additionalProperties":false,
		"properties":{
			"cmd":{"type":"string","description":"command"},
			"env":{"type":"object","additionalProperties":{"type":"string"}},
			"steps":{"type":"array","items":{
				"type":"object",
				"additionalProperties":false,
				"properties":{"run":{"type":"string"}},
				"required":["run"]
			}}
		},
		"required":["cmd"]
	}`
	_, err := g.Chat(context.Background(), ChatRequest{
		Messages: []Message{TextMessage(RoleUser, "x")},
		Tools:    []Tool{{Name: "verify", Description: "run checks", InputSchema: json.RawMessage(schema)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	decl := got.Tools[0].FunctionDeclarations[0]
	raw, _ := json.Marshal(decl.Parameters)
	for _, banned := range []string{"additionalProperties", "$schema"} {
		if strings.Contains(string(raw), banned) {
			t.Errorf("sanitized schema still contains %q:\n%s", banned, raw)
		}
	}
	// Semantics-bearing structure survives.
	var m map[string]any
	if err := json.Unmarshal(decl.Parameters, &m); err != nil {
		t.Fatalf("sanitized schema not valid JSON: %v", err)
	}
	props := m["properties"].(map[string]any)
	if _, ok := props["cmd"]; !ok {
		t.Error("cmd property lost")
	}
	steps := props["steps"].(map[string]any)
	items := steps["items"].(map[string]any)
	if _, ok := items["properties"].(map[string]any)["run"]; !ok {
		t.Error("nested items.properties.run lost")
	}
	if req, ok := m["required"].([]any); !ok || len(req) != 1 {
		t.Errorf("required lost: %v", m["required"])
	}
	// The env map became a plain object (closure keyword stripped) but the
	// property itself survives.
	if _, ok := props["env"]; !ok {
		t.Error("env property lost entirely — strip must remove the KEY, not the node")
	}
}

// TestGeminiThoughtSignatureRoundTrip: Gemini 3.x thinking models attach a
// thoughtSignature to functionCall parts and REQUIRE it echoed when the call
// is replayed in history (live failure: request 1 fine, request 2 400
// "missing a thought_signature"). The adapter captures it into
// ToolUse.Signature and replays it on the wire.
func TestGeminiThoughtSignatureRoundTrip(t *testing.T) {
	var got gemRequest
	srv := geminiTestServer(t, `{
		"candidates":[{"content":{"role":"model","parts":[
			{"functionCall":{"name":"bd","args":{"q":"show"}},"thoughtSignature":"sig-abc123"}
		]},"finishReason":"STOP"}]
	}`, &got)
	defer srv.Close()
	g := NewGemini(GeminiConfig{APIKey: "k", Model: "m", BaseURL: srv.URL})

	// Capture: the signature lands on the parsed ToolUse.
	resp, err := g.Chat(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "x")}})
	if err != nil {
		t.Fatal(err)
	}
	tus := resp.ToolUses()
	if len(tus) != 1 || tus[0].Signature != "sig-abc123" {
		t.Fatalf("thoughtSignature not captured: %+v", tus)
	}

	// Replay: the signature rides the functionCall part back to the wire.
	_, err = g.Chat(context.Background(), ChatRequest{Messages: []Message{
		TextMessage(RoleUser, "x"),
		{Role: RoleAssistant, Content: []ContentBlock{{Type: BlockToolUse, ToolUse: &tus[0]}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: BlockToolResult, ToolResult: &ToolResult{ToolUseID: tus[0].ID, Content: "ok"}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	fc := got.Contents[1].Parts[0]
	if fc.FunctionCall == nil || fc.ThoughtSignature != "sig-abc123" {
		t.Fatalf("replayed functionCall must carry the signature: %+v", fc)
	}
}

// TestGeminiThoughtSignatureBypassWhenAbsent: histories that predate the fix
// (or arrived via /model switch from another provider) have no signature —
// send Google's documented validator-skip token instead of omitting the field
// (omission hard-400s on thinking models).
func TestGeminiThoughtSignatureBypassWhenAbsent(t *testing.T) {
	var got gemRequest
	srv := geminiTestServer(t, `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`, &got)
	defer srv.Close()
	g := NewGemini(GeminiConfig{APIKey: "k", Model: "m", BaseURL: srv.URL})
	_, err := g.Chat(context.Background(), ChatRequest{Messages: []Message{
		{Role: RoleAssistant, Content: []ContentBlock{{Type: BlockToolUse, ToolUse: &ToolUse{ID: "c1", Name: "bd", Input: json.RawMessage(`{}`)}}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: BlockToolResult, ToolResult: &ToolResult{ToolUseID: "c1", Content: "ok"}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	fc := got.Contents[0].Parts[0]
	if fc.ThoughtSignature != "skip_thought_signature_validator" {
		t.Fatalf("absent signature must send the validator-skip token, got %q", fc.ThoughtSignature)
	}
	// Text parts never carry a signature slot.
	if len(got.Contents) > 1 {
		for _, p := range got.Contents[1].Parts {
			if p.ThoughtSignature != "" {
				t.Error("non-functionCall parts must not carry signatures")
			}
		}
	}
}
