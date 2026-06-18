package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

const emptyStatePrompt = "Describe what you want to build, or point me at a repo or backlog."

// TestConversationEmptyState: a fresh Conversation shows the empty-state prompt
// (PRD UI Navigation — Conversation default pane).
func TestConversationEmptyState(t *testing.T) {
	m := NewConversation(orchestrator.New())
	view := m.View()
	if !strings.Contains(view, emptyStatePrompt) {
		t.Fatalf("empty-state view should contain the prompt %q; got:\n%s", emptyStatePrompt, view)
	}
}

// TestConversationAcceptsAndEchoesIntent is the interactive done-gate for or-0d2:
// typing an intent and pressing Enter accepts it and echoes a confirmation.
func TestConversationAcceptsAndEchoesIntent(t *testing.T) {
	m := NewConversation(orchestrator.New())
	const intent = "Build an HTTP service that returns the current time."
	m.input.SetValue(intent)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Conversation)

	view := m.View()
	if !strings.Contains(view, intent) {
		t.Fatalf("view should echo the submitted intent; got:\n%s", view)
	}
	if !strings.Contains(view, "Got it") {
		t.Fatalf("view should show the Conductor confirmation; got:\n%s", view)
	}
	if m.input.Value() != "" {
		t.Fatalf("input should be cleared after submit; got %q", m.input.Value())
	}
}

// TestConversationEmptyIntentNotEchoed: pressing Enter with no input does not
// produce a confirmation (no silent acceptance of an empty intent).
func TestConversationEmptyIntentNotEchoed(t *testing.T) {
	m := NewConversation(orchestrator.New())
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Conversation)
	if strings.Contains(m.View(), "Got it") {
		t.Fatal("empty Enter should not yield a confirmation")
	}
}

// TestConversationQuit: Ctrl+C requests quit.
func TestConversationQuit(t *testing.T) {
	m := NewConversation(orchestrator.New())
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Conversation)
	if !m.quitting {
		t.Fatal("Ctrl+C should set quitting")
	}
	if cmd == nil {
		t.Fatal("Ctrl+C should return a quit command")
	}
}
