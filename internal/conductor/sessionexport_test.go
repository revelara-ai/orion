package conductor

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
)

// TestSessionExportHelpers (or-8my7 S3): the CLI-facing wrappers list persisted
// sessions and render a transcript from the faithful log (recall without the TUI).
func TestSessionExportHelpers(t *testing.T) {
	st := openStore(t)
	a := NewOrionAgent(nil, orchestrator.NewWithStore(st), RoleTemplate{})
	msgs := convo("recall me later")
	a.sessions["s"] = msgs
	a.persistSession("s", msgs)
	dir := a.sessionsDir()
	stamp := a.sessionStamp("s")

	infos, err := ListSessions(dir)
	if err != nil || len(infos) != 1 || infos[0].Stamp != stamp {
		t.Fatalf("ListSessions must surface the persisted session: %+v err=%v", infos, err)
	}
	if infos[0].Intent != "recall me later" {
		t.Fatalf("listing must carry the intent excerpt, got %q", infos[0].Intent)
	}

	got, err := ResolveSession(dir, "recall") // no such stamp; falls through, but "s" id won't match "recall"
	if err == nil {
		t.Fatalf("a non-matching fragment must not resolve, got %q", got)
	}
	stampResolved, err := ResolveSession(dir, stamp)
	if err != nil || stampResolved != stamp {
		t.Fatalf("exact stamp must resolve: %q err=%v", stampResolved, err)
	}

	md, err := RenderSessionTranscript(dir, stamp)
	if err != nil {
		t.Fatal(err)
	}
	// The transcript is the human view — it shows the intent + the tool call.
	if !strings.Contains(md, "recall me later") || !strings.Contains(md, "bash") {
		t.Fatalf("rendered transcript must reflect the session:\n%s", md)
	}
}
