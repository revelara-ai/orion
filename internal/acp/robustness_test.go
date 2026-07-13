package acp

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// blockingWriter's first Write signals `entered` then parks until `gate` is
// released — it simulates a peer that has stopped reading, stalling the writer.
type blockingWriter struct {
	entered chan struct{}
	gate    chan struct{}
	once    sync.Once
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.gate
	return len(p), nil
}

// TestStalledWriteDoesNotWedgeWritersForever (or-f06): the write path is a
// bounded queue drained by one writer goroutine, so a stalled peer write must
// NOT block other writers forever — once the connection tears down (peer
// disconnect), an enqueue-blocked write returns an error instead of hanging.
// (The happy path — writes succeed and round-trip — is covered by the
// Call/Notify tests in acp_test.go, which would fail if write always errored.)
func TestStalledWriteDoesNotWedgeWritersForever(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate) // release the parked writer goroutine when the test ends
	bw := &blockingWriter{entered: make(chan struct{}), gate: gate}

	pr, pw := io.Pipe() // the read side; closing pw tears the conn down
	conn := NewConn(pr, bw, nil, nil)
	go conn.Run(context.Background())

	// Frame 1 is taken by the writer goroutine, which enters Write and parks.
	if err := conn.Notify("f1", nil); err != nil {
		t.Fatalf("first write: %v", err)
	}
	<-bw.entered // the writer is now stalled mid-Write

	// Fill the queue to capacity so the NEXT enqueue must block.
	for i := 0; i < cap(conn.writeCh); i++ {
		if err := conn.Notify("fill", nil); err != nil {
			t.Fatalf("fill write %d: %v", i, err)
		}
	}

	// This write must block — the queue is full and the writer is stalled.
	res := make(chan error, 1)
	go func() { res <- conn.Notify("after", nil) }()
	select {
	case <-res:
		t.Fatal("write returned while the queue was full behind a stalled writer — it must apply backpressure")
	case <-time.After(150 * time.Millisecond):
	}

	// Tear the connection down (peer disconnect). The blocked write must
	// unblock with an ERROR, not stay wedged behind the stalled writer.
	_ = pw.Close()
	select {
	case err := <-res:
		if err == nil {
			t.Fatal("a write unblocked by teardown must return an error, not report success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("write stayed wedged after teardown — a single stalled writer serialize-blocks all writers (the bug)")
	}
}

// TestCloseWithoutRunReclaimsWriter (or-f06): the writer goroutine is started in
// NewConn, so a Conn that is built but never Run must be Closable to reclaim it.
// Close is idempotent (double-close of the done channel is guarded) and writes
// after Close error out — proving teardown fired and the writer goroutine exits.
func TestCloseWithoutRunReclaimsWriter(t *testing.T) {
	conn := NewConn(strings.NewReader(""), io.Discard, nil, nil)
	conn.Close()
	conn.Close() // idempotent: must not panic on a second teardown
	if err := conn.Notify("x", nil); err == nil {
		t.Fatal("a write after Close must return an error (the queue is torn down)")
	}
}

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
