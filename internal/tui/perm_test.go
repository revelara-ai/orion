package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/revelara-ai/orion/internal/acp"
)

// A Kind:"tool" permission renders an approval card with the tool, a colorized preview,
// and the three single-key choices; 'a' resolves it as allow_always.
func TestToolPermissionCardAndAllowAlways(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 70, Height: 20})
	reply := make(chan acp.PermissionResult, 1)
	m = feed(m, permMsg{req: acp.PermissionRequest{Kind: "tool", Tool: "edit_file", Preview: "x.go\n-old line\n+new line"}, reply: reply})

	card := ansi.Strip(m.renderTranscript())
	for _, want := range []string{"permission", "edit_file", "old line", "new line", "allow once", "allow always", "deny"} {
		if !strings.Contains(card, want) {
			t.Errorf("tool-permission card missing %q:\n%s", want, card)
		}
	}
	m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if res := <-reply; res.Outcome != "allow_always" {
		t.Errorf("'a' should resolve allow_always, got %q", res.Outcome)
	}
	if m.pendingPerm != nil || m.permKind != "" {
		t.Error("the pending permission should be cleared after answering")
	}
}

func TestToolPermissionSingleKeys(t *testing.T) {
	cases := map[string]string{"y": "allow_once", "n": "deny"}
	for key, want := range cases {
		m := newTestConvo(t)
		reply := make(chan acp.PermissionResult, 1)
		m = feed(m, permMsg{req: acp.PermissionRequest{Kind: "tool", Tool: "bash", Preview: "$ ls"}, reply: reply})
		m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if res := <-reply; res.Outcome != want {
			t.Errorf("key %q should resolve %q, got %q", key, want, res.Outcome)
		}
	}
}

// While a tool-permission card is up, ordinary keys don't leak into the input box, and
// Esc denies (safe default).
func TestToolPermissionCapturesInputAndEscDenies(t *testing.T) {
	m := newTestConvo(t)
	reply := make(chan acp.PermissionResult, 1)
	m = feed(m, permMsg{req: acp.PermissionRequest{Kind: "tool", Tool: "bash", Preview: "$ ls"}, reply: reply})
	m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")}) // ignored, not typed
	if v := m.input.Value(); v != "" {
		t.Errorf("keys must not leak into the input while a tool card is up, got %q", v)
	}
	m = feed(m, tea.KeyMsg{Type: tea.KeyEsc})
	if res := <-reply; res.Outcome != "deny" {
		t.Errorf("Esc should deny, got %q", res.Outcome)
	}
}

// 'e' toggles the preview expansion for a long diff (progressive disclosure).
func TestToolPermissionExpand(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 70, Height: 30})
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("+added line placeholder\n")
	}
	reply := make(chan acp.PermissionResult, 1)
	m = feed(m, permMsg{req: acp.PermissionRequest{Kind: "tool", Tool: "write_file", Preview: b.String()}, reply: reply})
	if !strings.Contains(m.renderTranscript(), "more · e expand") {
		t.Fatal("a long preview should truncate with an expand hint")
	}
	m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if strings.Contains(m.renderTranscript(), "more · e expand") {
		t.Error("'e' should expand the preview (hint gone)")
	}
	if m.pendingPerm == nil {
		t.Error("expanding must not resolve the permission")
	}
	reply <- acp.PermissionResult{Outcome: "deny"} // drain
}
