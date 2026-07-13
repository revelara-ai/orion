package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeAgent speaks JSON-RPC on the agent end of a pipe. handle answers each
// inbound request/notification; it may use send() to stream notifications.
type fakeAgent struct {
	conn net.Conn
	enc  *json.Encoder
	wmu  sync.Mutex
}

func (f *fakeAgent) send(m Message) {
	m.JSONRPC = "2.0"
	f.wmu.Lock()
	defer f.wmu.Unlock()
	_ = f.enc.Encode(&m)
}

func (f *fakeAgent) reply(id json.RawMessage, result any) {
	b, _ := json.Marshal(result)
	f.send(Message{ID: id, Result: b})
}

// startClient wires a Client to a fake agent over net.Pipe and runs both loops.
func startClient(t *testing.T, gate PermissionGate, fs SandboxFS, agentLoop func(f *fakeAgent, in *Message)) *Client {
	t.Helper()
	clientEnd, agentEnd := net.Pipe()
	t.Cleanup(func() { clientEnd.Close(); agentEnd.Close() })

	c := NewClient(clientEnd, clientEnd, gate, fs)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)

	f := &fakeAgent{conn: agentEnd, enc: json.NewEncoder(agentEnd)}
	go func() {
		sc := bufio.NewScanner(agentEnd)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			var m Message
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				continue
			}
			agentLoop(f, &m)
		}
	}()
	return c
}

// TestACPClientPromptTurnStreamsUpdates: a prompt turn streams session/update
// notifications to the client's sink, then completes with the prompt response.
func TestACPClientPromptTurnStreamsUpdates(t *testing.T) {
	agentLoop := func(f *fakeAgent, in *Message) {
		switch in.Method {
		case "session/new":
			f.reply(in.ID, map[string]string{"sessionId": "s1"})
		case "session/prompt":
			// Stream three updates, then end the turn.
			for _, txt := range []string{"thinking", "planning", "writing code"} {
				f.send(Message{Method: "session/update", Params: mustJSON(Update{SessionID: "s1", Kind: "agent_thought", Text: txt})})
			}
			f.reply(in.ID, PromptResult{StopReason: "end_turn"})
		}
	}
	c := startClient(t, nil, nil, agentLoop)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sid, err := c.SessionNew(ctx)
	if err != nil || sid != "s1" {
		t.Fatalf("session/new: id=%q err=%v", sid, err)
	}
	var got []string
	res, err := c.Prompt(ctx, sid, "build a thing", func(u Update) { got = append(got, u.Text) })
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if res.StopReason != "end_turn" {
		t.Fatalf("stop reason = %q", res.StopReason)
	}
	if len(got) != 3 || got[0] != "thinking" || got[2] != "writing code" {
		t.Fatalf("updates not streamed in order: %v", got)
	}
}

// TestRequestPermissionRoutesToGate: an agent→client session/request_permission
// is routed to Orion's gate, and the gate's decision is returned to the agent.
func TestRequestPermissionRoutesToGate(t *testing.T) {
	gate := &recordingGate{outcome: "granted"}
	done := make(chan PermissionResult, 1)
	agentLoop := func(f *fakeAgent, in *Message) {
		if in.Method == "session/prompt" {
			// During the turn the agent asks for permission (a request, with id).
			f.send(Message{ID: json.RawMessage(`100`), Method: "session/request_permission",
				Params: mustJSON(PermissionRequest{SessionID: "s1", Title: "delete prod", Kind: "destructive"})})
			return
		}
		// The client's response to our request 100 (a response: id + result).
		if in.Method == "" && string(in.ID) == "100" {
			var r PermissionResult
			_ = json.Unmarshal(in.Result, &r)
			done <- r
		}
	}
	c := startClient(t, gate, nil, agentLoop)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Fire a prompt to trigger the agent's permission request; the turn won't
	// "complete", so run Prompt in a goroutine and assert on the gate routing.
	go c.Prompt(ctx, "s1", "go", func(Update) {})

	select {
	case r := <-done:
		if r.Outcome != "granted" {
			t.Fatalf("agent got outcome %q, want granted", r.Outcome)
		}
	case <-ctx.Done():
		t.Fatal("permission request never routed/answered")
	}
	if gate.calls != 1 || gate.lastKind != "destructive" {
		t.Fatalf("gate not invoked correctly: calls=%d kind=%q", gate.calls, gate.lastKind)
	}
}

// TestFsTerminalSandboxScoped: fs reads/writes inside the worktree succeed; paths
// outside the scope (e.g. the held-out corpus) are rejected, and terminal exec is
// not a bypass.
func TestFsTerminalSandboxScoped(t *testing.T) {
	root := t.TempDir()
	corpus := t.TempDir() // a sibling "held-out corpus" — must be unreachable
	_ = os.WriteFile(filepath.Join(root, "in_scope.txt"), []byte("hello"), 0o644)
	_ = os.WriteFile(filepath.Join(corpus, "secret.txt"), []byte("corpus"), 0o644)
	fs := ScopedFS{Root: root}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Here the AGENT calls the client; use a raw conn on the agent end to issue
	// fs/terminal requests and read the client's responses.
	clientEnd, agentEnd := net.Pipe()
	defer clientEnd.Close()
	defer agentEnd.Close()
	srv := NewClient(clientEnd, clientEnd, nil, fs)
	go srv.Run(ctx)
	agent := NewConn(agentEnd, agentEnd, nil, nil)
	go agent.Run(ctx)

	// In-scope read succeeds.
	var rd struct {
		Content string `json:"content"`
	}
	if err := agent.Call(ctx, "fs/read_text_file", map[string]string{"path": "in_scope.txt"}, &rd); err != nil {
		t.Fatalf("in-scope read failed: %v", err)
	}
	if rd.Content != "hello" {
		t.Fatalf("read content = %q", rd.Content)
	}

	// Out-of-scope read (absolute path into the corpus) is rejected.
	if err := agent.Call(ctx, "fs/read_text_file", map[string]string{"path": filepath.Join(corpus, "secret.txt")}, &rd); err == nil {
		t.Fatal("out-of-scope corpus read must be rejected")
	}
	// Traversal escape is rejected.
	if err := agent.Call(ctx, "fs/read_text_file", map[string]string{"path": "../../etc/passwd"}, &rd); err == nil {
		t.Fatal("path traversal must be rejected")
	}
	// In-scope write succeeds and lands inside root.
	if err := agent.Call(ctx, "fs/write_text_file", map[string]string{"path": "out/new.txt", "content": "x"}, nil); err != nil {
		t.Fatalf("in-scope write failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "out", "new.txt")); err != nil {
		t.Fatalf("write did not land in scope: %v", err)
	}
	// Terminal exec is not a bypass.
	if err := agent.Call(ctx, "terminal/create", map[string]any{"command": "rm", "args": []string{"-rf", "/"}}, nil); err == nil {
		t.Fatal("terminal exec must be gated, not a bypass")
	}
}

type recordingGate struct {
	mu       sync.Mutex
	calls    int
	lastKind string
	outcome  string
}

func (g *recordingGate) RequestPermission(_ context.Context, req PermissionRequest) (PermissionResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	g.lastKind = req.Kind
	return PermissionResult{Outcome: g.outcome}, nil
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// TestPromptSinkSurvivesStaleTurnTeardown (or-5g2q): when two prompt turns
// overlap (cancel-then-resubmit), the OLDER turn returning must not clear the
// NEWER turn's update sink. Forces the interleaving deterministically: A sets
// its sink, B sets its sink, A tears down, THEN a live update is delivered — it
// must reach B, not be dropped.
func TestPromptSinkSurvivesStaleTurnTeardown(t *testing.T) {
	ready := make(chan *fakeAgent, 1)
	promptIDs := make(chan json.RawMessage, 4)
	agentLoop := func(f *fakeAgent, in *Message) {
		switch in.Method {
		case "session/new":
			f.reply(in.ID, map[string]string{"sessionId": "s1"})
		case "session/prompt":
			select {
			case ready <- f: // publish the agent handle once
			default:
			}
			promptIDs <- in.ID // hold the turn open (no reply yet)
		}
	}
	c := startClient(t, nil, nil, agentLoop)
	ctx := context.Background()
	if _, err := c.SessionNew(ctx); err != nil {
		t.Fatal(err)
	}

	aGot := make(chan string, 8)
	aDone := make(chan struct{})
	go func() { _, _ = c.Prompt(ctx, "s1", "A", func(u Update) { aGot <- u.Text }); close(aDone) }()
	idA := <-promptIDs
	f := <-ready

	bGot := make(chan string, 8)
	go func() { _, _ = c.Prompt(ctx, "s1", "B", func(u Update) { bGot <- u.Text }) }()
	idB := <-promptIDs // B has now installed its sink (the request is on the wire)

	// Turn A returns; wait until its deferred teardown has actually run.
	f.reply(idA, PromptResult{StopReason: "end_turn"})
	<-aDone

	// A live update for turn B — must reach B's sink, not be dropped by A's teardown.
	f.send(Message{Method: "session/update", Params: mustJSON(Update{SessionID: "s1", Kind: "agent_message", Text: "FOR_B"})})
	select {
	case got := <-bGot:
		if got != "FOR_B" {
			t.Fatalf("update routed wrong: %q", got)
		}
	case <-aGot:
		t.Fatal("update reached the stale turn A's sink")
	case <-time.After(2 * time.Second):
		t.Fatal("turn B's sink was clobbered by turn A's teardown — the live update was dropped (the bug)")
	}
	f.reply(idB, PromptResult{StopReason: "end_turn"}) // let B finish
}

// TestSessionCancelReachesAllOverlappingTurns (or-or3j): the agent keys cancel
// funcs per TURN, not per session-slot — with two overlapping prompts on one
// session (a foreign client may overlap; Orion's TUI serializes), a
// session/cancel stops BOTH, and one turn finishing must not orphan the
// other's cancel (the old single-slot map let prompt 1's deferred delete
// remove prompt 2's cancel func, making prompt 2 uncancellable).
func TestSessionCancelReachesAllOverlappingTurns(t *testing.T) {
	blockA, blockB := make(chan struct{}), make(chan struct{})
	doneA, doneB := make(chan error, 1), make(chan error, 1)
	prompt := func(ctx context.Context, _, text string, _ func(Update), _ AskFunc) (PromptResult, error) {
		gate := blockA
		if text == "B" {
			gate = blockB
		}
		select {
		case <-ctx.Done():
			return PromptResult{StopReason: "cancelled"}, ctx.Err()
		case <-gate:
			return PromptResult{StopReason: "end_turn"}, nil
		}
	}
	a := &Agent{prompt: prompt, cancels: map[string]map[int64]context.CancelFunc{}}

	run := func(text string, done chan error) {
		_, err := a.handle(context.Background(), "session/prompt",
			json.RawMessage(`{"sessionId":"s1","text":"`+text+`"}`))
		done <- err
	}
	go run("A", doneA)
	go run("B", doneB)
	// Wait until both turns are registered (both gates parked).
	for i := 0; ; i++ {
		a.mu.Lock()
		n := len(a.cancels["s1"])
		a.mu.Unlock()
		if n == 2 {
			break
		}
		if i > 200 {
			t.Fatalf("both overlapping turns should register a cancel, have %d", n)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Turn A finishes normally — its cleanup must NOT orphan turn B's cancel.
	close(blockA)
	if err := <-doneA; err != nil {
		t.Fatalf("turn A: %v", err)
	}

	// session/cancel must still stop the (still-running) turn B.
	a.onNotify("session/cancel", json.RawMessage(`{"sessionId":"s1"}`))
	select {
	case err := <-doneB:
		if err == nil {
			t.Fatal("turn B finished without cancellation — the cancel was orphaned")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("turn B never saw the cancel — the single-slot map orphaned it (the bug)")
	}

	// Negative: the registry drains — no leaked cancel entries after both end.
	a.mu.Lock()
	left := len(a.cancels["s1"])
	a.mu.Unlock()
	if left != 0 {
		t.Fatalf("cancel registry must be empty after both turns end, has %d", left)
	}
}

// With BOTH turns still live, one session/cancel stops them all — not just an
// arbitrary one (the "cancel the newest" regression).
func TestSessionCancelStopsBothLiveTurns(t *testing.T) {
	never := make(chan struct{}) // neither turn finishes on its own
	done := make(chan error, 2)
	prompt := func(ctx context.Context, _, _ string, _ func(Update), _ AskFunc) (PromptResult, error) {
		select {
		case <-ctx.Done():
			return PromptResult{StopReason: "cancelled"}, ctx.Err()
		case <-never:
			return PromptResult{}, nil
		}
	}
	a := &Agent{prompt: prompt, cancels: map[string]map[int64]context.CancelFunc{}}
	for i := 0; i < 2; i++ {
		go func() {
			_, err := a.handle(context.Background(), "session/prompt", json.RawMessage(`{"sessionId":"s1","text":"x"}`))
			done <- err
		}()
	}
	for i := 0; ; i++ {
		a.mu.Lock()
		n := len(a.cancels["s1"])
		a.mu.Unlock()
		if n == 2 {
			break
		}
		if i > 200 {
			t.Fatal("both turns should register")
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.onNotify("session/cancel", json.RawMessage(`{"sessionId":"s1"}`))
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("a live turn finished uncancelled")
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("turn %d never saw the session/cancel — cancel must reach ALL live turns", i+1)
		}
	}
}
