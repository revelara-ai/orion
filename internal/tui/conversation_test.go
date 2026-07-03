package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

// newTestConvo builds a sized Conversation with no live client (synthetic-message
// tests drive Update directly; the async prompt path is covered by the conductor
// + acceptance suites).
func newTestConvo(t *testing.T) Conversation {
	t.Helper()
	m := NewConversation(nil, "s1", orchestrator.New(), &programGate{})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return next.(Conversation)
}

func feed(m Conversation, msg tea.Msg) Conversation {
	next, _ := m.Update(msg)
	return next.(Conversation)
}

func transcript(m Conversation) string {
	var b strings.Builder
	for _, mm := range m.msgs {
		b.WriteString(mm.text)
		b.WriteByte('\n')
	}
	return b.String()
}

// TestConversationEmptyState: the fresh pane shows the empty-state prompt.
func TestConversationEmptyState(t *testing.T) {
	m := newTestConvo(t)
	if !strings.Contains(m.View(), emptyState) {
		t.Fatalf("empty-state prompt missing:\n%s", m.View())
	}
}

// TestConversationStreamsUpdates: a streamed session/update is appended to the
// transcript as it arrives (incremental streaming).
func TestConversationStreamsUpdates(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, streamMsg{u: acp.Update{Kind: "agent_message", Text: "[functional] Which port?"}})
	m = feed(m, streamMsg{u: acp.Update{Kind: "spec", Text: "functional port=8080"}})
	if !strings.Contains(transcript(m), "Which port") {
		t.Fatalf("streamed question not rendered:\n%s", transcript(m))
	}
	if !strings.Contains(transcript(m), "port=8080") {
		t.Fatalf("streamed spec not rendered:\n%s", transcript(m))
	}
}

// TestConversationPermissionGate: a permission request surfaces an approval card
// and the human's 'y' resolves the gate's reply channel with 'granted' — the
// blocking ratify gate, driven from the UI without deadlock.
func TestConversationPermissionGate(t *testing.T) {
	m := newTestConvo(t)
	reply := make(chan acp.PermissionResult, 1)
	m = feed(m, permMsg{req: acp.PermissionRequest{Kind: "spec_ratify", Title: "Ratify the assembled spec?"}, reply: reply})

	if m.pendingPerm == nil {
		t.Fatal("permission request did not set a pending reply")
	}
	if !strings.Contains(m.View(), "ratify") {
		t.Fatalf("approval card not rendered:\n%s", m.View())
	}

	// Human answers 'y' → the gate's reply channel receives 'granted'.
	m.input.SetValue("y")
	m = feed(m, tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case res := <-reply:
		if res.Outcome != "granted" {
			t.Fatalf("gate got %q, want granted", res.Outcome)
		}
	default:
		t.Fatal("gate reply channel never received the decision")
	}
	if m.pendingPerm != nil {
		t.Fatal("pending permission not cleared after answer")
	}
}

// TestConversationPermissionDenyOnEdit: any non-'y' answer denies (so the
// developer can then edit a field).
func TestConversationPermissionDenyOnEdit(t *testing.T) {
	m := newTestConvo(t)
	reply := make(chan acp.PermissionResult, 1)
	m = feed(m, permMsg{req: acp.PermissionRequest{Kind: "spec_ratify"}, reply: reply})
	m.input.SetValue("e")
	m = feed(m, tea.KeyMsg{Type: tea.KeyEnter})
	res := <-reply
	if res.Outcome != "denied" {
		t.Fatalf("non-y answer should deny, got %q", res.Outcome)
	}
}

// TestConversationEmptyInputNotSent: an empty Enter adds nothing and dispatches
// no command.
func TestConversationEmptyInputNotSent(t *testing.T) {
	m := newTestConvo(t)
	before := len(m.msgs)
	m.input.SetValue("   ")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Conversation)
	if len(m.msgs) != before || cmd != nil {
		t.Fatalf("empty input produced output: msgs=%d cmd=%v", len(m.msgs), cmd)
	}
}

// TestConversationQuit: Ctrl+C quits and (if a permission is pending) unblocks the
// gate goroutine with a denial.
func TestConversationQuit(t *testing.T) {
	m := newTestConvo(t)
	reply := make(chan acp.PermissionResult, 1)
	m = feed(m, permMsg{req: acp.PermissionRequest{}, reply: reply})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD}) // Ctrl+D is the exit key
	if !next.(Conversation).quitting || cmd == nil {
		t.Fatal("ctrl+d should quit")
	}
	if res := <-reply; res.Outcome != "denied" {
		t.Fatal("quit must unblock a pending gate with a denial")
	}
}

// TestSpendIsSurfacedLiveInTUI: the always-on budget spend renders live.
func TestSpendIsSurfacedLiveInTUI(t *testing.T) {
	m := newTestConvo(t)
	if !strings.Contains(m.View(), "spend:") {
		t.Fatalf("spend line missing:\n%s", m.View())
	}
	m.oc.Budget().Record(1234, 0.56)
	if v := m.View(); !strings.Contains(v, "1234 tok") || !strings.Contains(v, "$0.56") {
		t.Fatalf("live spend not surfaced:\n%s", v)
	}
}
