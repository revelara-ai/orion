package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/revelara-ai/orion/internal/acp"
)

// wrappedRows is the height oracle for the auto-growing input: the number of
// display rows a value occupies once soft-wrapped to the given content width.
func TestWrappedRows(t *testing.T) {
	cases := []struct {
		name  string
		s     string
		width int
		want  int
	}{
		{"empty is one row", "", 10, 1},
		{"short fits one row", "hello", 10, 1},
		{"three hard lines", "a\nb\nc", 10, 3},
		{"width floor never panics", "x", 0, 1},
	}
	for _, c := range cases {
		if got := wrappedRows(c.s, c.width); got != c.want {
			t.Errorf("%s: wrappedRows(%q, %d) = %d, want %d", c.name, c.s, c.width, got, c.want)
		}
	}

	// A line longer than the width must wrap to more than one row (the property
	// the single-line textinput lacked). Use >= to stay robust to the exact
	// word-wrap boundary.
	long := strings.Repeat("wx ", 12) // 36 cols of spaced words
	if got := wrappedRows(long, 8); got < 3 {
		t.Errorf("long line at width 8: wrappedRows = %d, want >= 3", got)
	}

	// wrappedRows must agree with how lipgloss itself lays the text out, so the
	// input box height matches the rendered content exactly (no over/underdraw).
	if got, want := wrappedRows(long, 12), lipgloss.Height(lipgloss.NewStyle().Width(12).Render(long)); got != want {
		t.Errorf("wrappedRows(%q,12) = %d, want lipgloss height %d", long, got, want)
	}
}

// Typing a line far longer than the input width must GROW the input box (the
// transcript viewport shrinks to make room) while the total layout still fills the
// terminal exactly — never overflowing or tearing. The single-line textinput could
// not do this: it scrolled horizontally and the viewport never reflowed.
func TestInputGrowsAndLayoutStaysExact(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 40, Height: 24})                    // narrow → typed text must wrap
	m = feed(m, streamMsg{u: acp.Update{Kind: "agent_message", Text: "hi"}}) // activate the transcript viewport
	m = feed(m, tea.WindowSizeMsg{Width: 40, Height: 24})                    // re-lay-out with content present

	if got := lipgloss.Height(m.View()); got != 24 {
		t.Fatalf("empty input: View height = %d, want 24", got)
	}
	baseVP := m.vp.Height

	long := strings.Repeat("alpha ", 20) // 120 cols, far past the ~32-col input width
	m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(long)})

	if m.input.Height() <= 1 {
		t.Fatalf("input box did not grow: height = %d, want > 1", m.input.Height())
	}
	if m.vp.Height >= baseVP {
		t.Fatalf("input did not grow: viewport height %d did not shrink from %d", m.vp.Height, baseVP)
	}
	if got := lipgloss.Height(m.View()); got != 24 {
		t.Fatalf("wrapped input: View height = %d, want 24 (layout overflowed/tore)", got)
	}

	// Clearing the input collapses the box back to one row and restores the viewport.
	m.input.Reset()
	m = feed(m, tea.WindowSizeMsg{Width: 40, Height: 24})
	if m.input.Height() != 1 {
		t.Fatalf("after clearing, input height = %d, want 1", m.input.Height())
	}
	if m.vp.Height != baseVP {
		t.Fatalf("after clearing, viewport height = %d, want restored %d", m.vp.Height, baseVP)
	}
}

// Guard: the conversation must forward mouse-wheel events to the transcript
// viewport (so the wheel scrolls history), never swallow them in the key switch.
func TestMouseWheelScrollsTranscript(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 60, Height: 12}) // short viewport → scrollable
	for i := range 40 {
		m = feed(m, streamMsg{u: acp.Update{Kind: "agent_message", Text: fmt.Sprintf("history line %d\n", i)}})
	}
	if !m.vp.AtBottom() {
		m.vp.GotoBottom()
	}
	// Wheel up must move the viewport off the bottom.
	m = feed(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if m.vp.AtBottom() {
		t.Fatal("wheel-up did not scroll the transcript viewport off the bottom")
	}
}
