package tui

import (
	"strings"
	"testing"
)

// TestToolPermCardShowsRationale (or-10m0): the approval card renders the
// assistant's rationale above the command preview, so the human sees WHY the
// prompt popped up. A card with no rationale still renders (title + preview).
func TestToolPermCardShowsRationale(t *testing.T) {
	m := Conversation{}
	m.vp.Width = 80

	withWhy := m.toolPermCard(msg{
		kind: "tool_permission", tool: "bash",
		text:      "$ rm stale.tmp",
		rationale: "removing the stale artifact before the rebuild",
	})
	if !strings.Contains(withWhy, "rebuild") || !strings.Contains(withWhy, "stale artifact") {
		t.Fatalf("the card must show the rationale above the preview:\n%s", withWhy)
	}
	if !strings.Contains(withWhy, "rm stale.tmp") {
		t.Fatalf("the card must still show the command preview:\n%s", withWhy)
	}

	// No rationale → card still renders with the tool + preview, no crash.
	noWhy := m.toolPermCard(msg{kind: "tool_permission", tool: "bash", text: "$ ls"})
	if !strings.Contains(noWhy, "ls") {
		t.Fatalf("a card without a rationale must still render the preview:\n%s", noWhy)
	}
}
