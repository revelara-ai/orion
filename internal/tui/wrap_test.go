package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// TestRenderWrapsToWidth: at a narrow terminal width, no rendered transcript line
// exceeds the width — message bodies, the spec/permission cards, and the
// developer's turn all wrap instead of flowing off the edge (or-6u1).
func TestRenderWrapsToWidth(t *testing.T) {
	long := strings.Repeat("the conductor asks a very detailed question about the spec ", 6)
	for _, w := range []int{24, 40, 80} {
		m := Conversation{width: w, msgs: []msg{
			{role: "you", text: long},
			{role: "orion", kind: "agent_message", text: long},
			{role: "orion", kind: "spec", text: "functional response_format=json, timezone=UTC, port=8080, route=/time\nscale scale_profile=medium (default)"},
			{role: "orion", kind: "permission", text: "Ratify the assembled spec?"},
			{role: "orion", kind: "plan", text: long},
		}}
		out := m.renderTranscript()
		for i, line := range strings.Split(out, "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Fatalf("width=%d: line %d exceeds it (visible %d): %q", w, i, got, line)
			}
		}
	}
}
