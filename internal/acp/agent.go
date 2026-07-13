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
// CONTRACT (or-or3j): a brain must NOT call stream after PromptFunc returns —
// the client treats the session/prompt response as "this turn stopped
// emitting" (its cancel drain gate rests on it). A goroutine that outlives the
// return and streams would leak its output into the NEXT turn's transcript.
//
// Deadlock note: ask issues an inbound REQUEST to the client; the client
// dispatches requests on their own goroutine (off its read loop), so the gate may
// block on a human without stalling the stream.
type PromptFunc func(ctx context.Context, sessionID, text string, stream func(Update), ask AskFunc) (PromptResult, error)

// AskFunc requests a client-side permission decision mid-turn.
type AskFunc func(req PermissionRequest) (PermissionResult, error)

// ControlFunc handles an out-of-turn session control op (compact | model | …) and
// returns a human-readable result. Optional: a nil control returns "unsupported".
type ControlFunc func(ctx context.Context, sessionID, op, arg string) (string, error)

// Agent is the ACP agent (server) side: the counterpart to Client. It answers
// initialize / session/new / session/prompt and streams session/update during a
// turn, and honors session/cancel.
type Agent struct {
	conn    *Conn
	prompt  PromptFunc
	control ControlFunc

	mu       sync.Mutex
	sessions int
	nextTurn int64
	// cancels is keyed per session AND per turn (or-or3j): a single per-session
	// slot let an overlapping second prompt overwrite the first's cancel — and
	// the first's deferred cleanup then deleted the SECOND's, leaving it
	// uncancellable. session/cancel stops every active turn of the session.
	cancels map[string]map[int64]context.CancelFunc
}

// NewAgent builds an ACP agent over the given reader/writer, driven by prompt.
func NewAgent(r io.Reader, w io.Writer, prompt PromptFunc) *Agent {
	a := &Agent{prompt: prompt, cancels: map[string]map[int64]context.CancelFunc{}}
	a.conn = NewConn(r, w, a.handle, a.onNotify)
	return a
}

// SetControl registers the handler for session/control ops (compact / model). Returns
// the agent for chaining. Nil control makes those ops report "unsupported".
func (a *Agent) SetControl(fn ControlFunc) *Agent { a.control = fn; return a }

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
		a.nextTurn++
		turnID := a.nextTurn
		if a.cancels[p.SessionID] == nil {
			a.cancels[p.SessionID] = map[int64]context.CancelFunc{}
		}
		a.cancels[p.SessionID][turnID] = cancel
		a.mu.Unlock()
		defer func() {
			a.mu.Lock()
			if turns := a.cancels[p.SessionID]; turns != nil {
				delete(turns, turnID) // only THIS turn's entry — never a sibling's
				if len(turns) == 0 {
					delete(a.cancels, p.SessionID)
				}
			}
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
	case "session/control":
		var p struct {
			SessionID string `json:"sessionId"`
			Op        string `json:"op"`
			Arg       string `json:"arg"`
		}
		_ = json.Unmarshal(params, &p)
		if a.control == nil {
			return map[string]string{"result": "that control is not supported by this brain."}, nil
		}
		res, err := a.control(ctx, p.SessionID, p.Op, p.Arg)
		if err != nil {
			return nil, err
		}
		return map[string]string{"result": res}, nil
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
	for _, cancel := range a.cancels[p.SessionID] {
		cancel() // every active turn of the session — not just the newest
	}
	a.mu.Unlock()
}
