package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/revelara-ai/orion/pkg/llmclient"
)

const okBody = `{"model":"m","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{}}`

// fastRetry replaces the provider's client with one that retries with negligible
// backoff (the production config uses real backoff; tests must not sleep seconds).
func fastRetry(timeout time.Duration) *llmclient.Client {
	return llmclient.New(llmclient.Config{Timeout: timeout, MaxRetries: 4, BaseBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond})
}

// TestAnthropicRetriesTransient5xx: a 503 (overloaded) is retried with backoff and
// the call eventually succeeds — the behavior missing when MaxRetries was 0.
func TestAnthropicRetriesTransient5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(503)
			_, _ = io.WriteString(w, "overloaded")
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, okBody)
	}))
	defer srv.Close()

	a := NewAnthropic("k", "m")
	a.baseURL = srv.URL
	a.rc = fastRetry(5 * time.Second)

	res, err := a.Chat(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "hi")}})
	if err != nil {
		t.Fatalf("chat should succeed after retrying 5xx: %v", err)
	}
	if res.Text() != "ok" {
		t.Fatalf("text = %q", res.Text())
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Fatalf("calls = %d, want 3 (two 503s then success)", n)
	}
}

// TestAnthropicRetriesTimeout: the exact failure the user hit — a slow first
// response exceeds the per-attempt timeout ("context deadline exceeded") — is now
// retried instead of surfaced, and a prompt response on retry succeeds.
func TestAnthropicRetriesTimeout(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			time.Sleep(600 * time.Millisecond) // first attempt blows the per-attempt deadline
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, okBody)
	}))
	defer srv.Close()

	a := NewAnthropic("k", "m")
	a.baseURL = srv.URL
	a.rc = fastRetry(150 * time.Millisecond)

	res, err := a.Chat(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "hi")}})
	if err != nil {
		t.Fatalf("chat should succeed after retrying a timeout: %v", err)
	}
	if res.Text() != "ok" {
		t.Fatalf("text = %q", res.Text())
	}
	if n := atomic.LoadInt32(&calls); n < 2 {
		t.Fatalf("calls = %d, want >=2 (timeout then retry)", n)
	}
}

// TestNewAnthropicConfiguresRetries guards against regressing to the zero-config
// (no-retry, 30s) client: a default provider must actually retry.
func TestNewAnthropicConfiguresRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) < 2 {
			w.Header().Set("retry-after", "0") // keep the test fast (no 2s default)
			w.WriteHeader(529)                 // overloaded (Retry-After path)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, okBody)
	}))
	defer srv.Close()

	a := NewAnthropic("k", "m") // production config — do NOT override a.rc
	a.baseURL = srv.URL
	if _, err := a.Chat(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "hi")}}); err != nil {
		t.Fatalf("default provider should retry a 529: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("calls = %d, want 2 (529 then success) — default config must retry", n)
	}
}
