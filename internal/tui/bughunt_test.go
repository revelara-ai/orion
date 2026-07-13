package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/revelara-ai/orion/internal/acp"
)

// TestPaletteArrowKeysCycleNotHistory (or-ns8 bug 7): while the command palette
// is open, ↑/↓ move the selection (as the footer advertises) instead of being
// hijacked by history recall — which would stash the draft and clobber the
// /-prefix input, closing the palette.
func TestPaletteArrowKeysCycleNotHistory(t *testing.T) {
	m := newTestConvo(t)
	m.commands = []Command{{Name: "clear"}, {Name: "clone"}}
	m.history = []string{"an old submitted line"} // non-empty so buggy history recall is observable
	m.histIdx = len(m.history)
	m.input.SetValue("/cl") // palette open: matches clear, clone

	m = feed(m, tea.KeyMsg{Type: tea.KeyDown})
	v := m.input.Value()
	if v == "an old submitted line" {
		t.Fatal("↓ recalled history instead of cycling the palette while it was open")
	}
	if !strings.HasPrefix(v, "/cl") {
		t.Fatalf("↓ should select a palette command (a /cl* value), got %q", v)
	}
	// ↑ also cycles (and stays in the palette, not history).
	m = feed(m, tea.KeyMsg{Type: tea.KeyUp})
	if v := m.input.Value(); !strings.HasPrefix(v, "/cl") {
		t.Fatalf("↑ should cycle the palette too, got %q", v)
	}

	// Negative: with NO palette open, ↑ STILL recalls history (the guard is
	// scoped to an open palette, not a blanket disable).
	m2 := newTestConvo(t)
	m2.history = []string{"recall me"}
	m2.histIdx = len(m2.history)
	m2 = feed(m2, tea.KeyMsg{Type: tea.KeyUp})
	if m2.input.Value() != "recall me" {
		t.Fatalf("with no palette open, ↑ must still recall history, got %q", m2.input.Value())
	}
}

// TestSpecRatifyCardHasNoEditAffordance (or-ns8 bug 8): a spec-ratify card only
// grants on 'y' and denies otherwise, so it must NOT advertise an 'edit'
// affordance that silently rejects — the placeholder and card show reject.
func TestSpecRatifyCardHasNoEditAffordance(t *testing.T) {
	m := newTestConvo(t)
	reply := make(chan acp.PermissionResult, 1)
	m = feed(m, permMsg{req: acp.PermissionRequest{Kind: "spec_ratify", Title: "Ratify the spec?"}, reply: reply})

	if strings.Contains(m.input.Placeholder, "edit") {
		t.Fatalf("spec-ratify placeholder must not advertise 'edit', got %q", m.input.Placeholder)
	}
	card := ansi.Strip(m.renderTranscript())
	if strings.Contains(card, "edit a field") {
		t.Fatalf("spec-ratify card must not advertise an unimplemented 'edit a field':\n%s", card)
	}
	if !strings.Contains(card, "reject") {
		t.Fatalf("spec-ratify card should make the reject path explicit:\n%s", card)
	}
	reply <- acp.PermissionResult{Outcome: "denied"} // drain the gate
}

// TestNewlineChordInertWhilePermissionCardUp (or-ns8 bug 12): the input is inert
// while a permission card is up, so the newline chord (Ctrl+J / Alt+Enter) must
// not edit it — and must not resolve the card.
func TestNewlineChordInertWhilePermissionCardUp(t *testing.T) {
	m := newTestConvo(t)
	reply := make(chan acp.PermissionResult, 1)
	m = feed(m, permMsg{req: acp.PermissionRequest{Kind: "tool", Tool: "bash", Preview: "$ ls"}, reply: reply})
	before := m.input.Value()

	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlJ}) // newline chord
	if m.input.Value() != before {
		t.Fatalf("Ctrl+J must not insert a newline into the inert input while a card is up, got %q", m.input.Value())
	}
	if !m.hasPerm() {
		t.Fatal("the newline chord must not resolve the pending permission")
	}
	m.answerToolPerm("deny") // drain the gate

	// Negative: with NO card up, the newline chord STILL inserts a newline (the
	// guard is scoped to an open card, not a blanket disable).
	m2 := newTestConvo(t)
	m2.input.SetValue("line one")
	m2 = feed(m2, tea.KeyMsg{Type: tea.KeyCtrlJ})
	if !strings.Contains(m2.input.Value(), "\n") {
		t.Fatalf("with no card up, Ctrl+J must still insert a newline, got %q", m2.input.Value())
	}
}
