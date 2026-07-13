package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

// TestIdleHintAdvertisesCopyAffordance: because the program grabs the mouse for
// wheel-scroll (WithMouseCellMotion), native drag-select is suppressed. The idle
// hint must tell the user how to still select for copy/paste (hold Shift, or ⌥ on
// macOS) — otherwise the terminal appears to have "lost" copy/paste.
func TestIdleHintAdvertisesCopyAffordance(t *testing.T) {
	view := strings.ToLower(newTestConvo(t).View())
	if !strings.Contains(view, "shift") || !strings.Contains(view, "copy") {
		t.Fatalf("idle hint must advertise shift-drag to copy; got:\n%s", newTestConvo(t).View())
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

	if !m.hasPerm() {
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
	if m.hasPerm() {
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

// TestActivityPaneSuppressedDuringPermission: a pending permission BLOCKS the turn
// on the human — inFlight stays true, but nothing is "working". The live activity
// pane must not render, or it wedges between the approval card and the y/a/n prompt
// (the collision).
func TestActivityPaneSuppressedDuringPermission(t *testing.T) {
	m := newTestConvo(t)
	m.inFlight = true
	m = feed(m, streamMsg{u: acp.Activity("Orion", "build_service", 0, "running")})
	if !strings.Contains(m.View(), "build_service") {
		t.Fatalf("precondition: in-flight activity pane should show the running activity:\n%s", m.View())
	}

	// A tool-permission request arrives mid-turn (the turn is now BLOCKED on the human).
	reply := make(chan acp.PermissionResult, 1)
	m = feed(m, permMsg{req: acp.PermissionRequest{Kind: "tool", Tool: "edit_file", Preview: "--- a\n+++ b"}, reply: reply})

	if strings.Contains(m.View(), "build_service") {
		t.Fatalf("activity pane must be suppressed while a permission is pending (it collides with the approval card):\n%s", m.View())
	}
	if !strings.Contains(m.View(), "edit_file") {
		t.Fatalf("permission card must still render:\n%s", m.View())
	}
	if got := lipgloss.Height(m.View()); got != 24 {
		t.Fatalf("layout not height-exact during permission: %d, want 24", got)
	}
}

// TestActivityPaneShowsStackThenCollapses: in-flight activity pane surfaces the
// subagent actor and stays height-exact; collapsing to idle removes the live pane.
func TestActivityPaneShowsStackThenCollapses(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.inFlight = true
	m = feed(m, streamMsg{u: acp.Activity("Orion", "build_service", 0, "running")})
	m = feed(m, streamMsg{u: acp.Activity("research", "web_search", 1, "running")})

	if !strings.Contains(m.View(), "research") {
		t.Fatalf("in-flight activity pane should show the subagent actor:\n%s", m.View())
	}
	if got := lipgloss.Height(m.View()); got != 24 {
		t.Fatalf("layout not height-exact with activity pane: %d, want 24", got)
	}

	m = feed(m, turnDoneMsg{})
	if strings.Contains(m.View(), "web_search") {
		t.Fatalf("idle view must collapse the live pane:\n%s", m.View())
	}
}

// TestIdleSummaryHeightExact: after a turn completes with at least one done phase, the
// activity model produces a non-empty one-line idle summary. The layout must still fill
// the terminal exactly (header + transcript + summary + input + hint == m.height rows).
func TestIdleSummaryHeightExact(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.inFlight = true
	// Feed a depth-0 known phase name to "done" so finish() produces a summary line.
	m = feed(m, streamMsg{u: acp.Activity("Orion", "Generate", 0, "done")})
	// turnDoneMsg calls activity.finish(), sets inFlight=false, and populates a.summary.
	m = feed(m, turnDoneMsg{})

	v := m.View()
	// Confirm the idle summary is present (proves the test actually exercises the summary path).
	if !strings.Contains(v, "Generate") {
		t.Fatalf("idle summary missing 'Generate' marker:\n%s", v)
	}
	// The layout must remain height-exact even with the one-line summary inserted.
	if got := lipgloss.Height(v); got != 24 {
		t.Fatalf("layout not height-exact with idle summary: %d, want 24\n%s", got, v)
	}
}

// TestInFlightPlusPaletteHeightExact: the highest-risk compound state — an in-flight
// activity pane AND the command palette both rendered simultaneously. The layout must
// still fill the terminal exactly.
func TestInFlightPlusPaletteHeightExact(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.inFlight = true
	// Build the activity stack so the in-flight pane renders.
	m = feed(m, streamMsg{u: acp.Activity("Orion", "build_service", 0, "running")})

	// Set the input to a bare "/" prefix — paletteMatches() matches all builtins
	// (help, clear, compact, context, model, exit) without needing injected commands.
	m.input.SetValue("/")

	v := m.View()
	// Confirm the activity pane is present.
	if !strings.Contains(v, "build_service") {
		t.Fatalf("in-flight activity pane missing 'build_service':\n%s", v)
	}
	// Confirm the command palette is present (at least one known builtin name visible).
	if !strings.Contains(v, "help") {
		t.Fatalf("command palette not rendered (expected 'help' entry):\n%s", v)
	}
	// Both panes must coexist without breaking height-exactness.
	if got := lipgloss.Height(v); got != 24 {
		t.Fatalf("layout not height-exact with in-flight pane + palette: %d, want 24\n%s", got, v)
	}
}
