package conductor

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/pkg/llm"
)

// TestSessionsAndResumeAcrossRestart (or-8my7 S2): /sessions lists a persisted
// session and /resume reloads it into a FRESH agent (a new process) so the
// conversation continues — the in-memory fork/clone family can't do this.
func TestSessionsAndResumeAcrossRestart(t *testing.T) {
	st := openStore(t)

	// "Process 1": run a session and let it persist to disk.
	a1 := NewOrionAgent(nil, orchestrator.NewWithStore(st), RoleTemplate{})
	msgs := convo("build a PvE mech game")
	a1.sessions["orig"] = msgs
	a1.persistSession("orig", msgs)
	stamp := a1.sessionStamp("orig")

	// "Process 2": a brand-new agent over the same store — no in-memory sessions.
	a2 := NewOrionAgent(nil, orchestrator.NewWithStore(st), RoleTemplate{})

	list, err := a2.Control(context.Background(), "new", "sessions", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, stamp) || !strings.Contains(list, "build a PvE mech game") {
		t.Fatalf("/sessions must list the persisted session + its intent:\n%s", list)
	}

	// Resume by stamp into the new agent's active session.
	res, err := a2.Control(context.Background(), "new", "resume", stamp)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !strings.Contains(res, "Resumed") {
		t.Fatalf("resume result: %q", res)
	}
	got := a2.sessions["new"]
	if len(got) != len(msgs) {
		t.Fatalf("resumed history must match: got %d want %d", len(got), len(msgs))
	}
	// Fidelity survived the round-trip through disk into a new process.
	if tu := got[1].Content[1].ToolUse; tu == nil || tu.ID != "tu_1" {
		t.Fatalf("resumed tool_use must keep its id: %+v", tu)
	}

	// Resume by an id/substring fragment resolves too.
	if _, err := a2.Control(context.Background(), "new2", "resume", "orig"); err != nil {
		t.Fatalf("resume by id fragment: %v", err)
	}
	// An unknown target is a clean error, not a crash.
	if _, err := a2.Control(context.Background(), "new3", "resume", "no-such-session"); err == nil {
		t.Fatal("resume of an unknown session must error")
	}
}

// TestResumeThenContinuePersistsIntoOwnLog (or-8my7 S2): after a resume, the
// next persist writes the FULL resumed history into the new session's own log
// (offset reset), and the original log is untouched.
func TestResumeThenContinuePersistsIntoOwnLog(t *testing.T) {
	st := openStore(t)
	a1 := NewOrionAgent(nil, orchestrator.NewWithStore(st), RoleTemplate{})
	msgs := convo("original")
	a1.sessions["orig"] = msgs
	a1.persistSession("orig", msgs)
	stamp := a1.sessionStamp("orig")

	a2 := NewOrionAgent(nil, orchestrator.NewWithStore(st), RoleTemplate{})
	if _, err := a2.Control(context.Background(), "cont", "resume", stamp); err != nil {
		t.Fatal(err)
	}
	// Continue the conversation and persist.
	cont := append(a2.sessions["cont"], llm.TextMessage(llm.RoleUser, "keep going"))
	a2.sessions["cont"] = cont
	a2.persistSession("cont", cont)

	// The new session's own log holds the full resumed history + the new turn.
	loaded, err := loadSessionHistory(a2.sessionsDir(), a2.sessionStamp("cont"))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != len(msgs)+1 {
		t.Fatalf("continuation log must hold resumed history + new turn: got %d want %d", len(loaded), len(msgs)+1)
	}
	// The original log is untouched.
	orig, err := loadSessionHistory(a1.sessionsDir(), stamp)
	if err != nil || len(orig) != len(msgs) {
		t.Fatalf("resume must not mutate the source log: got %d want %d err=%v", len(orig), len(msgs), err)
	}
}
