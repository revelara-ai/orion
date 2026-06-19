package acp

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// TestServeRecoversFromHandlerPanic: a panic in a request handler must not drop
// the response — the caller's Call returns an error instead of hanging forever.
func TestServeRecoversFromHandlerPanic(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	server := NewConn(a, a, func(context.Context, string, json.RawMessage) (any, error) {
		panic("boom")
	}, nil)
	go server.Run(ctx)
	caller := NewConn(b, b, nil, nil)
	go caller.Run(ctx)

	err := caller.Call(ctx, "anything", map[string]string{}, nil)
	if err == nil {
		t.Fatal("Call should return an error when the handler panics, not hang")
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Fatalf("error should surface the panic: %v", err)
	}
}

// TestRunLoopFreedByClose: cancelling ctx does NOT unblock a read loop blocked in
// net.Pipe.Read — only Close does. This is the invariant tui.Run relies on
// (deferred Close on every path) to avoid leaking read-loop goroutines.
func TestRunLoopFreedByClose(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	conn := NewConn(a, a, nil, nil)
	go func() { _ = conn.Run(ctx); close(done) }()

	// Cancel alone must NOT free the loop (it's blocked in Read between Scans).
	cancel()
	select {
	case <-done:
		t.Fatal("read loop exited on ctx cancel alone — net.Pipe Read is not ctx-aware; the test's premise is wrong")
	case <-time.After(150 * time.Millisecond):
	}

	// Close frees it.
	_ = a.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("read loop did not exit after Close — Run would leak the goroutine")
	}
}
