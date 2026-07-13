// Package acp implements the Agent Client Protocol transport (SPEC §2,§4): a
// JSON-RPC 2.0 peer over stdio. Orion's TUI/Conductor is the ACP *client* — it
// drives a spawned agent (initialize / session/new / session/prompt / cancel,
// consuming session/update streams) and simultaneously *serves* the
// agent-exposed client methods (session/request_permission, fs/*, terminal/*)
// under Orion's safety gates. Messages are newline-delimited JSON.
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// Message is a JSON-RPC 2.0 envelope. A request has Method+ID; a notification has
// Method and no ID; a response has ID and Result|Error.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// Handler answers an inbound request from the peer (returns a result or error).
type Handler func(ctx context.Context, method string, params json.RawMessage) (any, error)

// Notifier receives an inbound notification from the peer.
type Notifier func(method string, params json.RawMessage)

// Conn is a bidirectional JSON-RPC 2.0 connection.
type Conn struct {
	w       io.Writer
	r       io.Reader
	handler Handler
	notify  Notifier

	// One writer goroutine owns c.w and drains writeCh in FIFO order, so no
	// caller ever holds a lock across a (possibly stalled) Write. done is
	// closed exactly once on teardown, unblocking every enqueuer.
	writeCh   chan []byte
	done      chan struct{}
	closeOnce sync.Once

	mu      sync.Mutex
	nextID  int
	pending map[int]chan *Message
	closed  bool
}

// writeQueueDepth bounds the outbound frame queue: a stalled peer applies
// backpressure at this depth rather than blocking writers unboundedly.
const writeQueueDepth = 64

// NewConn builds a connection over the given reader/writer. handler answers
// inbound requests; notify receives inbound notifications. Either may be nil.
func NewConn(r io.Reader, w io.Writer, handler Handler, notify Notifier) *Conn {
	c := &Conn{
		r: r, w: w, handler: handler, notify: notify,
		pending: map[int]chan *Message{},
		writeCh: make(chan []byte, writeQueueDepth),
		done:    make(chan struct{}),
	}
	go c.writeLoop()
	return c
}

// writeLoop is the SOLE owner of c.w: it drains queued frames one at a time so
// a stalled Write blocks only this goroutine (never a caller holding a lock).
// A write error tears the connection down, which unblocks every pending caller.
func (c *Conn) writeLoop() {
	for {
		select {
		case b := <-c.writeCh:
			if _, err := c.w.Write(b); err != nil {
				c.teardown()
				return
			}
		case <-c.done:
			return
		}
	}
}

// Run reads and dispatches messages until the reader closes or ctx is done.
func (c *Conn) Run(ctx context.Context) error {
	sc := bufio.NewScanner(c.r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var m Message
		if err := json.Unmarshal(line, &m); err != nil {
			continue // skip malformed frames rather than tearing down the loop
		}
		c.dispatch(ctx, &m)
	}
	c.teardown()
	return sc.Err()
}

func (c *Conn) dispatch(ctx context.Context, m *Message) {
	switch {
	case m.Method != "" && len(m.ID) > 0: // inbound request
		go c.serve(ctx, m)
	case m.Method != "": // inbound notification
		if c.notify != nil {
			c.notify(m.Method, m.Params)
		}
	default: // response to one of our calls
		if id, err := strconv.Atoi(string(m.ID)); err == nil {
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
}

func (c *Conn) serve(ctx context.Context, m *Message) {
	// A panic in a handler must not silently drop the response — that would block
	// the caller's Call forever. Recover and answer with an error.
	defer func() {
		if r := recover(); r != nil {
			_ = c.write(&Message{JSONRPC: "2.0", ID: m.ID, Error: &RPCError{Code: -32603, Message: fmt.Sprintf("handler panic: %v", r)}})
		}
	}()
	resp := Message{JSONRPC: "2.0", ID: m.ID}
	if c.handler == nil {
		resp.Error = &RPCError{Code: -32601, Message: "method not found: " + m.Method}
		_ = c.write(&resp)
		return
	}
	result, err := c.handler(ctx, m.Method, m.Params)
	if err != nil {
		resp.Error = &RPCError{Code: -32000, Message: err.Error()}
		_ = c.write(&resp)
		return
	}
	if result != nil {
		if b, e := json.Marshal(result); e == nil {
			resp.Result = b
		}
	}
	_ = c.write(&resp)
}

// Call sends a request and blocks for the response, unmarshalling result into out.
func (c *Conn) Call(ctx context.Context, method string, params any, out any) error {
	var praw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		praw = b
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("acp: connection closed")
	}
	c.nextID++
	id := c.nextID
	ch := make(chan *Message, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	req := Message{JSONRPC: "2.0", ID: json.RawMessage(strconv.Itoa(id)), Method: method, Params: praw}
	if err := c.write(&req); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case resp := <-ch:
		if resp == nil {
			return fmt.Errorf("acp: connection closed before response")
		}
		if resp.Error != nil {
			return resp.Error
		}
		if out != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	}
}

// Notify sends a notification (no response expected).
func (c *Conn) Notify(method string, params any) error {
	var praw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		praw = b
	}
	return c.write(&Message{JSONRPC: "2.0", Method: method, Params: praw})
}

// write marshals m and hands the frame to the writer goroutine. It never
// touches c.w directly, so a stalled peer applies backpressure (a full queue)
// rather than serialize-blocking every writer — and once the connection tears
// down, a queued-blocked write unblocks with an error instead of hanging.
func (c *Conn) write(m *Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	select {
	case <-c.done:
		return fmt.Errorf("acp: connection closed")
	default:
	}
	select {
	case c.writeCh <- b:
		return nil
	case <-c.done:
		return fmt.Errorf("acp: connection closed")
	}
}

// Close tears the connection down and reclaims the writer goroutine. Safe to
// call repeatedly, and safe whether or not Run was started: a caller that
// builds a Conn but never Run()s it MUST Close it to avoid leaking the writer
// goroutine (Run's read-loop exit closes it automatically otherwise).
func (c *Conn) Close() { c.teardown() }

// teardown closes the connection exactly once: it stops the writer goroutine
// (via done) and fails every pending Call so no caller hangs. Idempotent, so
// both the read loop's exit and a writer-goroutine error can call it.
func (c *Conn) teardown() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.mu.Lock()
		c.closed = true
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
	})
}
