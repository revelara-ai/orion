package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revelara-ai/orion/internal/acp"
)

// /clear empties the transcript (a fresh context); /exit quits; /context shows usage.
func TestIntrinsicCommands(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, streamMsg{u: acp.Update{Kind: "agent_message", Text: "some history"}})
	if len(m.msgs) == 0 {
		t.Fatal("setup: expected transcript content")
	}

	// /clear wipes the transcript.
	m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/clear")})
	m = feed(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.msgs) != 0 {
		t.Errorf("/clear should empty the transcript, got %d msgs", len(m.msgs))
	}

	// /context reports token usage.
	m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/context")})
	m = feed(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(transcript(m), "tok") {
		t.Errorf("/context should report token usage:\n%s", transcript(m))
	}

	// /exit quits.
	m = feed(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/exit")})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !next.(Conversation).quitting {
		t.Error("/exit should quit")
	}
}

// The intrinsic commands appear in /help and the command palette.
func TestIntrinsicCommandsInHelpAndPalette(t *testing.T) {
	m := newTestConvo(t)
	help := m.commandHelp()
	for _, name := range []string{"clear", "context", "exit"} {
		if !strings.Contains(help, "/"+name) {
			t.Errorf("/help missing intrinsic command /%s:\n%s", name, help)
		}
	}
	m.input.SetValue("/cl")
	matches := m.paletteMatches()
	found := false
	for _, c := range matches {
		if c.Name == "clear" {
			found = true
		}
	}
	if !found {
		t.Errorf("palette should offer /clear for prefix /cl, got %+v", matches)
	}
}

// Typing "@<prefix>" offers matching file paths from the working directory; Tab completes
// the selection into the input. (Test cwd is internal/tui, so "@con" matches conversation.go.)
func TestAtFileAutocomplete(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.input.SetValue("look at @con")

	_, matches := m.atCompletions()
	if len(matches) == 0 {
		t.Fatal("@con should offer file completions")
	}
	found := false
	for _, f := range matches {
		if strings.HasPrefix(f, "con") {
			found = true
		}
	}
	if !found {
		t.Errorf("@con should match a con*.go file, got %v", matches)
	}
	// Tab completes the @token to a full @path — the '@' sigil is PRESERVED so
	// the completed token stays a recognized file directive on submit (or-ns8;
	// dropping the @ silently disables the inline-file expansion).
	m = feed(m, tea.KeyMsg{Type: tea.KeyTab})
	v := m.input.Value()
	if !strings.HasPrefix(v, "look at @") {
		t.Errorf("Tab must keep the @ sigil on the completed path, got %q", v)
	}
	if !strings.Contains(v, ".go") {
		t.Errorf("completed value should contain a file path, got %q", v)
	}
	if v == "look at @con" {
		t.Errorf("Tab should have extended the @con stub to a full path, got %q", v)
	}
}

// A bare "@" with no file token, or a non-@ input, offers nothing (no false popup).
func TestAtCompletionsOnlyOnAtToken(t *testing.T) {
	m := newTestConvo(t)
	m.input.SetValue("just a normal sentence")
	if _, matches := m.atCompletions(); len(matches) != 0 {
		t.Errorf("plain prose must not trigger @ completion, got %v", matches)
	}
}

// /compact and /model appear in help + palette, and dispatch gracefully with no client.
func TestCompactAndModelCommands(t *testing.T) {
	m := newTestConvo(t)
	help := m.commandHelp()
	for _, name := range []string{"compact", "model"} {
		if !strings.Contains(help, "/"+name) {
			t.Errorf("/help missing /%s:\n%s", name, help)
		}
	}
	// controlCmd with no live client resolves to a graceful message, never a panic.
	if msg := m.controlCmd("compact", "")(); func() bool {
		cr, ok := msg.(CommandResultMsg)
		return !ok || !strings.Contains(cr.Text, "not connected")
	}() {
		t.Errorf("controlCmd with no client should report gracefully, got %v", msg)
	}
}

// The status chrome shows the working directory and a context/usage indicator.
func TestStatusChromeShowsCwdAndContext(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m = feed(m, streamMsg{u: acp.Update{Kind: "agent_message", Text: "hi"}})
	v := m.View()
	// or-un7z: the cumulative figure is labeled 'total' — 'ctx' implied
	// window occupancy and sent a developer chasing an impossible number.
	if !strings.Contains(v, "total") {
		t.Errorf("status chrome should show the cumulative-total indicator:\n%s", v)
	}
}
