package tui

import (
	"strings"
	"testing"
)

// TestSessionSentinelSwitchesBranch (or-ykz.5): a SESSION:<id> command result
// moves the TUI's active ACP session to the new branch, and only the display
// text reaches the transcript.
func TestSessionSentinelSwitchesBranch(t *testing.T) {
	m := Conversation{sid: "s1", width: 80}
	mm, _ := m.Update(CommandResultMsg{Text: "SESSION:s1-f1 · forked s1 at turn 2/3"})
	conv, ok := mm.(Conversation)
	if !ok {
		t.Fatalf("update must return the Conversation, got %T", mm)
	}
	if conv.sid != "s1-f1" {
		t.Fatalf("SESSION sentinel must switch the active branch, sid=%q", conv.sid)
	}
	last := conv.msgs[len(conv.msgs)-1]
	if strings.Contains(last.text, "SESSION:") || !strings.Contains(last.text, "forked s1") {
		t.Fatalf("transcript must carry only the display text, got %q", last.text)
	}
}

// TestForkCommandForwardsAsControlOp: /fork rides the same control plumbing as
// /compact — with no client it degrades to a message, never a panic.
func TestForkCommandForwardsAsControlOp(t *testing.T) {
	m := newTestConvo(t)
	help := m.commandHelp()
	for _, name := range []string{"fork", "clone", "tree", "switch"} {
		if !strings.Contains(help, "/"+name) {
			t.Errorf("/help missing /%s:\n%s", name, help)
		}
	}
	cmd := m.handleCommand("/fork 2")
	if cmd == nil {
		t.Fatal("/fork must return a control command")
	}
	if res, ok := cmd().(CommandResultMsg); !ok || !strings.Contains(res.Text, "not connected") {
		t.Fatalf("no-client fork must degrade gracefully, got %#v", cmd())
	}
}
