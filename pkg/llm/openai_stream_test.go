package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func oaSseServer(t *testing.T, events []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		for _, e := range events {
			w.Write([]byte("data: " + e + "\n\n"))
		}
	}))
}

func TestOpenAIChatStreamAssemblesTextAndTools(t *testing.T) {
	srv := oaSseServer(t, []string{
		`{"choices":[{"delta":{"content":"Hel"}}]}`,
		`{"choices":[{"delta":{"content":"lo"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_9","function":{"name":"ls","arguments":"{\"pa"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\".\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":7,"completion_tokens":4}}`,
		`[DONE]`,
	})
	defer srv.Close()
	o := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "m"})

	var chunks []string
	resp, err := o.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "x")}}, func(s string) {
		chunks = append(chunks, s)
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if got := strings.Join(chunks, ""); got != "Hello" {
		t.Errorf("streamed text = %q, want Hello", got)
	}
	if resp.Text() != "Hello" {
		t.Errorf("assembled text = %q", resp.Text())
	}
	tus := resp.ToolUses()
	if len(tus) != 1 || tus[0].ID != "call_9" || tus[0].Name != "ls" || string(tus[0].Input) != `{"path":"."}` {
		t.Fatalf("assembled tool use wrong: %+v", tus)
	}
	if resp.StopReason != StopToolUse {
		t.Errorf("stop = %q, want tool_use", resp.StopReason)
	}
	if resp.Usage.InputTokens != 7 || resp.Usage.OutputTokens != 4 {
		t.Errorf("usage wrong: %+v", resp.Usage)
	}
}

func TestOpenAIChatStreamTruncated(t *testing.T) {
	// Stream ends without finish_reason or [DONE] → must error, never a silent partial.
	srv := oaSseServer(t, []string{`{"choices":[{"delta":{"content":"par"}}]}`})
	defer srv.Close()
	o := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "m"})
	_, err := o.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "x")}}, nil)
	if err == nil {
		t.Fatal("truncated stream must return an error")
	}
}
