package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/revelara-ai/orion/pkg/llmclient"
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

// TestOpenAIChatStreamNoRetryAfterEmit: a mid-body read error AFTER text has been
// emitted must be terminal — never retried, so already-shown output is not
// duplicated. This is the exact regression shape: the server emits one delta then
// stalls past the per-attempt deadline, so the client's Body.Read fails with an
// error satisfying errors.Is(err, context.DeadlineExceeded) — precisely what
// net/http returns when llmclient's per-attempt context times out mid-read. If
// doStream's emitted-path error were error-chain-transparent (%w) instead of
// severed (%v), llmclient's retryable() would match through it via
// errors.Is(err, context.DeadlineExceeded) and llmclient.Do would retry into
// duplicate output — a CloseClientConnections-style abrupt disconnect would NOT
// catch this, since that produces a plain (non-DeadlineExceeded) read error that
// was already non-retryable even before the fix.
func TestOpenAIChatStreamNoRetryAfterEmit(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+`{"choices":[{"delta":{"content":"partial"}}]}`+"\n\n")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		// Stall until the client's per-attempt context is canceled (deadline fires
		// mid-body-read), then let the handler return — the client's Read on
		// resp.Body observes the deadline as the read error.
		<-r.Context().Done()
	}))
	defer srv.Close()
	o := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "m"})
	// A short per-attempt timeout so the deadline fires quickly against the
	// handler's stall, with retries enabled (MaxRetries>0) so an accidental
	// retryable classification would be observable as hits>1.
	o.rc = llmclient.New(llmclient.Config{Timeout: 50 * time.Millisecond, MaxRetries: 4, BaseBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond})

	var got string
	_, err := o.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "x")}},
		func(s string) { got += s })
	if err == nil {
		t.Fatal("expected a terminal error after a mid-stream deadline failure")
	}
	if got != "partial" {
		t.Fatalf("emitted text = %q, want the partial before the failure", got)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("hits = %d, want 1 (no retry after emit — would duplicate output)", n)
	}
}

// TestOpenAIChatStreamRetriesBeforeEmit: a 500 before any data is retried and the
// stream then succeeds — retries are still allowed when nothing has been shown yet.
func TestOpenAIChatStreamRetriesBeforeEmit(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		for _, e := range []string{
			`{"choices":[{"delta":{"content":"Hello"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`[DONE]`,
		} {
			_, _ = io.WriteString(w, "data: "+e+"\n\n")
		}
	}))
	defer srv.Close()
	o := NewOpenAI(OpenAIConfig{BaseURL: srv.URL + "/v1", Model: "m"})
	o.rc = fastRetry(5 * time.Second)

	res, err := o.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "x")}}, func(string) {})
	if err != nil {
		t.Fatalf("stream should retry a pre-emit 500: %v", err)
	}
	if res.Text() != "Hello" {
		t.Fatalf("text = %q", res.Text())
	}
	if n := atomic.LoadInt32(&hits); n != 2 {
		t.Fatalf("hits = %d, want 2 (500 then success)", n)
	}
}
