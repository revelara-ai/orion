package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/revelara-ai/orion/internal/acp"
)

// Ctrl+C while a turn is in flight CANCELS it without exiting the app.
func TestCtrlCCancelsInFlightWithoutExit(t *testing.T) {
	m := newTestConvo(t)
	m.inFlight = true
	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.quitting {
		t.Error("Ctrl+C during a turn must NOT quit")
	}
	if m.inFlight {
		t.Error("Ctrl+C during a turn must cancel it (inFlight=false)")
	}
}

// Esc cancels streaming (an in-flight turn) without exiting.
func TestEscCancelsStreamingWithoutExit(t *testing.T) {
	m := newTestConvo(t)
	m.inFlight = true
	m = feed(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.quitting {
		t.Error("Esc must not quit")
	}
	if m.inFlight {
		t.Error("Esc must cancel the in-flight turn")
	}
}

// Ctrl+C with text in the box clears the box (shell-style), doesn't exit or arm quit.
func TestCtrlCClearsNonEmptyInput(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("draft text")})
	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.quitting {
		t.Error("Ctrl+C with a draft must not quit")
	}
	if strings.TrimSpace(m.input.Value()) != "" {
		t.Errorf("Ctrl+C should clear the draft, got %q", m.input.Value())
	}
}

// At an idle empty prompt, the FIRST Ctrl+C arms; a SECOND exits.
func TestDoubleCtrlCExitsFromEmptyPrompt(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.quitting {
		t.Fatal("first Ctrl+C at an empty prompt must NOT quit (arm only)")
	}
	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.quitting {
		t.Fatal("second Ctrl+C must quit")
	}
}

// Any keypress between the two Ctrl+C disarms the pending quit.
func TestKeypressDisarmsQuit(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlC})                     // arm
	m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}) // disarm
	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlC})                     // non-empty now → clears, not quit
	if m.quitting {
		t.Error("a keypress must disarm the pending double-Ctrl+C quit")
	}
}

// Ctrl+D exits immediately.
func TestCtrlDExits(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlD})
	if !m.quitting {
		t.Error("Ctrl+D must quit")
	}
}

// A multi-line paste stays a single draft (its internal newlines don't submit), and a
// huge paste never panics.
func TestMultiLinePasteStaysOneInput(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 60, Height: 20})
	m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line1\nline2\nline3"), Paste: true})
	if v := m.input.Value(); !strings.Contains(v, "line1") || !strings.Contains(v, "line3") {
		t.Errorf("paste should be inserted whole, got %q", v)
	}
	if len(m.msgs) != 0 {
		t.Errorf("a paste must not submit, got %d msgs", len(m.msgs))
	}
	// Huge paste: must not panic and the layout still fills the terminal (seed a message
	// so the transcript viewport — not the short empty-state banner — drives the height).
	m = feed(m, streamMsg{u: acp.Update{Kind: "agent_message", Text: "hi"}})
	m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("wide paste text ", 500)), Paste: true})
	if h := lipgloss.Height(m.View()); h != 20 {
		t.Errorf("huge paste broke the layout: View height %d != 20", h)
	}
}

// Ctrl+C during a ratification prompt denies the gate and cancels — it does NOT exit.
func TestCtrlCDeniesPendingPermissionWithoutExit(t *testing.T) {
	m := newTestConvo(t)
	reply := make(chan acp.PermissionResult, 1)
	m = feed(m, permMsg{req: acp.PermissionRequest{}, reply: reply})
	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.quitting {
		t.Error("Ctrl+C during a ratification prompt should cancel it, not quit")
	}
	if res := <-reply; res.Outcome != "denied" {
		t.Errorf("Ctrl+C should deny the pending permission gate, got %q", res.Outcome)
	}
}

// Alt+Enter inserts a newline into the multi-line input instead of submitting.
func TestAltEnterInsertsNewline(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line one")})
	m = feed(m, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("line two")})
	if v := m.input.Value(); !strings.Contains(v, "\n") {
		t.Errorf("Alt+Enter should insert a newline, got %q", v)
	}
	if len(m.msgs) != 0 {
		t.Errorf("Alt+Enter must not submit the message, got %d msgs", len(m.msgs))
	}
}
