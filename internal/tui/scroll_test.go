package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revelara-ai/orion/internal/acp"
)

// TestPermissionCardForcedIntoViewWhenScrolledUp: a permission request BLOCKS the
// conversation and asks the developer to act (press y/e), so its card must be visible
// even if they had scrolled up to re-read history. Otherwise they're prompted to
// respond with no visible prompt — the bug the hunt surfaced. The viewport is forced
// to the bottom when the request arrives (normal messages keep the sticky-bottom
// behavior and do NOT yank a scrolled-up reader).
func TestPermissionCardForcedIntoViewWhenScrolledUp(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 60, Height: 12}) // short viewport → scrollable
	for i := 0; i < 30; i++ {
		m = feed(m, streamMsg{u: acp.Update{Kind: "agent_message", Text: fmt.Sprintf("history line %d\n", i)}})
	}
	m.vp.GotoTop() // scroll away from the tail
	if m.vp.AtBottom() {
		t.Fatal("test setup: not enough content to scroll off the bottom")
	}

	reply := make(chan acp.PermissionResult, 1)
	m = feed(m, permMsg{req: acp.PermissionRequest{Kind: "spec_ratify", Title: "Ratify the assembled spec?"}, reply: reply})

	if !m.vp.AtBottom() {
		t.Fatal("permission card not scrolled into view — developer is prompted to press y/e with no visible prompt")
	}
	if !strings.Contains(m.vp.View(), "Ratify") {
		t.Fatalf("ratify card not visible in the viewport body:\n%s", m.vp.View())
	}
}
