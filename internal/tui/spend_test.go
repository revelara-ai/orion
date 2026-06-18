package tui

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
)

// TestSpendIsSurfacedLiveInTUI: the always-on budget accountant's spend is
// rendered live in the Conversation pane — the view reflects spend recorded
// after the pane was built.
func TestSpendIsSurfacedLiveInTUI(t *testing.T) {
	c := orchestrator.New()
	m := NewConversation(c)

	// Initially zero spend is shown.
	if !strings.Contains(m.View(), "spend:") {
		t.Fatalf("spend line missing from view:\n%s", m.View())
	}

	// Record spend AFTER building the pane; the next render must reflect it (live).
	c.Budget().Record(1234, 0.56)
	view := m.View()
	if !strings.Contains(view, "1234 tok") {
		t.Fatalf("live token spend not surfaced; got:\n%s", view)
	}
	if !strings.Contains(view, "$0.56") {
		t.Fatalf("live dollar spend not surfaced; got:\n%s", view)
	}
}
