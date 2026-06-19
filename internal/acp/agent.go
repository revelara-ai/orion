package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// PromptFunc runs one prompt turn: it may stream session/update notifications via
// stream, and may request a client-side authorization mid-turn via ask (a
// blocking session/request_permission call back to the client). It returns the
// terminal result. This is where an agent's reasoning lives (in production, the
// spawned vendor agent — here, a Go implementation such as the primed Conductor).
//
// Deadlock note: ask issues an inbound REQUEST to the client; the client
// dispatches requests on their own goroutine (off its read loop), so the gate may
// block on a human without stalling the stream.
type PromptFunc func(ctx context.Context, sessionID, text string, stream func(Update), ask AskFunc) (PromptResult, error)

// AskFunc requests a client-side permission decision mid-turn.
type AskFunc func(req PermissionRequest) (PermissionResult, error)

// Agent is the ACP agent (server) side: the counterpart to Client. It answers
// initialize / session/new / session/prompt and streams session/update during a
// turn, and honors session/cancel.
type Agent struct {
	conn   *Conn
	prompt PromptFunc

	mu       sync.Mutex
	sessions int
	cancels  map[string]context.CancelFunc
}

// NewAgent builds an ACP agent over the given reader/writer, driven by prompt.
func NewAgent(r io.Reader, w io.Writer, prompt PromptFunc) *Agent {
	a := &Agent{prompt: prompt, cancels: map[string]context.CancelFunc{}}
	a.conn = NewConn(r, w, a.handle, a.onNotify)
	return a
}

// Run drives the agent's read loop; call in a goroutine.
func (a *Agent) Run(ctx context.Context) error { return a.conn.Run(ctx) }

func (a *Agent) handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "initialize":
		return map[string]any{"protocolVersion": 1}, nil
	case "session/new", "session/load":
		a.mu.Lock()
		a.sessions++
		sid := fmt.Sprintf("s%d", a.sessions)
		a.mu.Unlock()
		return map[string]string{"sessionId": sid}, nil
	case "session/prompt":
		var p struct {
			SessionID string `json:"sessionId"`
			Text      string `json:"text"`
		}
		_ = json.Unmarshal(params, &p)
		if a.prompt == nil {
			return PromptResult{StopReason: "end_turn"}, nil
		}
		turnCtx, cancel := context.WithCancel(ctx)
		a.mu.Lock()
		a.cancels[p.SessionID] = cancel
		a.mu.Unlock()
		defer func() {
			a.mu.Lock()
			delete(a.cancels, p.SessionID)
			a.mu.Unlock()
			cancel()
		}()
		stream := func(u Update) {
			u.SessionID = p.SessionID
			_ = a.conn.Notify("session/update", u)
		}
		ask := func(req PermissionRequest) (PermissionResult, error) {
			req.SessionID = p.SessionID
			var res PermissionResult
			err := a.conn.Call(turnCtx, "session/request_permission", req, &res)
			return res, err
		}
		return a.prompt(turnCtx, p.SessionID, p.Text, stream, ask)
	}
	return nil, fmt.Errorf("acp agent: method not found: %s", method)
}

func (a *Agent) onNotify(method string, params json.RawMessage) {
	if method != "session/cancel" {
		return
	}
	var p struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(params, &p)
	a.mu.Lock()
	if cancel := a.cancels[p.SessionID]; cancel != nil {
		cancel()
	}
	a.mu.Unlock()
}
