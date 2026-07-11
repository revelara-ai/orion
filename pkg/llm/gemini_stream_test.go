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

func TestGeminiChatStreamAssembles(t *testing.T) {
	srv := oaSseServer(t, []string{
		`{"candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"text":"lo"}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"ls","args":{"d":"."}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":3}}`,
	})
	defer srv.Close()
	g := NewGemini(GeminiConfig{APIKey: "k", Model: "m", BaseURL: srv.URL})

	var chunks []string
	resp, err := g.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "x")}}, func(s string) {
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
	if len(tus) != 1 || tus[0].Name != "ls" || tus[0].ID != "call_ls_1" {
		t.Fatalf("tool uses wrong: %+v", tus)
	}
	if resp.StopReason != StopToolUse {
		t.Errorf("stop = %q, want tool_use", resp.StopReason)
	}
	if resp.Usage.InputTokens != 9 || resp.Usage.OutputTokens != 3 {
		t.Errorf("usage wrong: %+v", resp.Usage)
	}
}

func TestGeminiChatStreamTruncated(t *testing.T) {
	srv := oaSseServer(t, []string{`{"candidates":[{"content":{"parts":[{"text":"par"}]}}]}`})
	defer srv.Close()
	g := NewGemini(GeminiConfig{APIKey: "k", Model: "m", BaseURL: srv.URL})
	_, err := g.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "x")}}, nil)
	if err == nil {
		t.Fatal("truncated stream must return an error")
	}
}

// TestGeminiChatStreamNoRetryAfterEmit: a mid-body read error AFTER text has been
// emitted must be terminal — never retried, so already-shown output is not
// duplicated. Modeled on TestOpenAIChatStreamNoRetryAfterEmit: the server emits one
// text chunk then stalls past the per-attempt deadline, so the client's Body.Read
// fails with an error satisfying errors.Is(err, context.DeadlineExceeded) — exactly
// what net/http returns when llmclient's per-attempt context times out mid-read. If
// doStream's emitted-path error were error-chain-transparent (%w) instead of severed
// (%v), llmclient's retryable() would match through it and llmclient.Do would retry
// into duplicate output.
func TestGeminiChatStreamNoRetryAfterEmit(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+`{"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}`+"\n\n")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		// Stall until the client's per-attempt context is canceled (deadline fires
		// mid-body-read), then let the handler return — the client's Read on
		// resp.Body observes the deadline as the read error.
		<-r.Context().Done()
	}))
	defer srv.Close()
	g := NewGemini(GeminiConfig{APIKey: "k", Model: "m", BaseURL: srv.URL})
	// A short per-attempt timeout so the deadline fires quickly against the
	// handler's stall, with retries enabled (MaxRetries>0) so an accidental
	// retryable classification would be observable as hits>1.
	g.rc = llmclient.New(llmclient.Config{Timeout: 50 * time.Millisecond, MaxRetries: 4, BaseBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond})

	var got string
	_, err := g.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "x")}},
		func(s string) { got += s })
	if err == nil {
		t.Fatal("expected a terminal error after a mid-stream deadline failure")
	}
	if got != "partial" {
		t.Fatalf("emitted text = %q, want the partial before the failure", got)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("hits = %d, want 1 (no retry after emit — would duplicate output)", n)
	}
}

// TestGeminiChatStreamRetriesBeforeEmit: a 500 before any data is retried and the
// stream then succeeds — retries are still allowed when nothing has been shown yet.
func TestGeminiChatStreamRetriesBeforeEmit(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		for _, e := range []string{
			`{"candidates":[{"content":{"parts":[{"text":"Hello"}]},"finishReason":"STOP"}]}`,
		} {
			_, _ = io.WriteString(w, "data: "+e+"\n\n")
		}
	}))
	defer srv.Close()
	g := NewGemini(GeminiConfig{APIKey: "k", Model: "m", BaseURL: srv.URL})
	g.rc = fastRetry(5 * time.Second)

	res, err := g.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "x")}}, func(string) {})
	if err != nil {
		t.Fatalf("stream should retry a pre-emit 500: %v", err)
	}
	if res.Text() != "Hello" {
		t.Fatalf("text = %q", res.Text())
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("hits = %d, want 2 (500 then success)", n)
	}
}
