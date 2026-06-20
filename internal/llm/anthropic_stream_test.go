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
)

// a text+tool_use streaming response (Anthropic SSE shape).
const sseTextAndTool = `event: message_start
data: {"type":"message_start","message":{"model":"claude-x","usage":{"input_tokens":11,"cache_read_input_tokens":5}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Which "}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"port?"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_9","name":"check_completeness"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"intent\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"svc\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}

event: message_stop
data: {"type":"message_stop"}

`

func sseServer(t *testing.T, handler http.HandlerFunc) *Anthropic {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	a := NewAnthropic("k", "claude-x")
	a.baseURL = srv.URL
	a.rc = fastRetry(5 * time.Second)
	return a
}

// TestAnthropicChatStreamParsesSSE: text deltas surface incrementally via onText,
// and the assembled response carries the full text, the tool_use (input assembled
// from input_json_delta fragments), the stop reason, and usage.
func TestAnthropicChatStreamParsesSSE(t *testing.T) {
	a := sseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, sseTextAndTool)
	})

	var deltas []string
	res, err := a.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "hi")}},
		func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if strings.Join(deltas, "") != "Which port?" || len(deltas) != 2 {
		t.Fatalf("deltas = %q (want incremental [\"Which \",\"port?\"])", deltas)
	}
	if res.Text() != "Which port?" {
		t.Fatalf("assembled text = %q", res.Text())
	}
	if res.StopReason != StopToolUse {
		t.Fatalf("stop = %q, want tool_use", res.StopReason)
	}
	tus := res.ToolUses()
	if len(tus) != 1 || tus[0].Name != "check_completeness" || tus[0].ID != "tu_9" {
		t.Fatalf("tool uses = %+v", tus)
	}
	if got := string(tus[0].Input); got != `{"intent":"svc"}` {
		t.Fatalf("tool input assembled = %q, want {\"intent\":\"svc\"}", got)
	}
	if res.Usage.InputTokens != 11 || res.Usage.OutputTokens != 7 || res.Usage.CacheReadInputTokens != 5 {
		t.Fatalf("usage = %+v", res.Usage)
	}
}

// TestAnthropicChatStreamRetriesBeforeEmit: a 503 before any data is retried and
// the stream then succeeds.
func TestAnthropicChatStreamRetriesBeforeEmit(t *testing.T) {
	var hits int32
	a := sseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, sseTextAndTool)
	})
	res, err := a.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "hi")}}, func(string) {})
	if err != nil {
		t.Fatalf("stream should retry a pre-emit 503: %v", err)
	}
	if res.Text() != "Which port?" {
		t.Fatalf("text = %q", res.Text())
	}
	if n := atomic.LoadInt32(&hits); n != 2 {
		t.Fatalf("hits = %d, want 2 (503 then success)", n)
	}
}

// TestAnthropicChatStreamTruncatedFails: a stream that ends cleanly (EOF) with no
// terminal event must be an error — never a silently-complete partial turn.
func TestAnthropicChatStreamTruncatedFails(t *testing.T) {
	var hits int32
	a := sseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, `event: message_start
data: {"type":"message_start","message":{"model":"m","usage":{}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"thinking"}}

`) // no content_block_stop, no message_delta, no message_stop — truncated
	})
	var got string
	_, err := a.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "hi")}},
		func(s string) { got += s })
	if err == nil {
		t.Fatal("truncated stream must return an error, not a silent partial turn")
	}
	if got != "thinking" {
		t.Fatalf("emitted = %q", got)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("hits = %d, want 1 (terminal after emit, no retry)", n)
	}
}

// TestAnthropicChatStreamTruncatedToolUseFails: a tool_use whose input JSON was cut
// off (even with a stop_reason) must fail — never dispatch a tool with half-formed
// arguments (the verifier reproduced `{"command":"sudo rm` reaching a tool).
func TestAnthropicChatStreamTruncatedToolUseFails(t *testing.T) {
	a := sseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, `event: message_start
data: {"type":"message_start","message":{"model":"m","usage":{}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"bash"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"sudo rm"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}

event: message_stop
data: {"type":"message_stop"}

`)
	})
	res, err := a.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "hi")}}, func(string) {})
	if err == nil {
		t.Fatalf("incomplete tool_use input must fail, got %d tool uses", len(res.ToolUses()))
	}
}

// TestAnthropicChatStreamNoRetryAfterEmit: an error AFTER text has been emitted is
// terminal — never retried, so already-shown output is not duplicated.
func TestAnthropicChatStreamNoRetryAfterEmit(t *testing.T) {
	var hits int32
	a := sseServer(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, `event: message_start
data: {"type":"message_start","message":{"model":"m","usage":{}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}

event: error
data: {"type":"error","error":{"message":"overloaded mid-stream"}}

`)
	})
	var got string
	_, err := a.ChatStream(context.Background(), ChatRequest{Messages: []Message{TextMessage(RoleUser, "hi")}},
		func(s string) { got += s })
	if err == nil {
		t.Fatal("expected a terminal error after a mid-stream failure")
	}
	if got != "partial" {
		t.Fatalf("emitted text = %q, want the partial before the error", got)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("hits = %d, want 1 (no retry after emit — would duplicate output)", n)
	}
}
