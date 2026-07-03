package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// TestResolveCommand (or-dz9): slash-commands dispatch to their handlers; /help lists them; an
// unknown command is reported (not forwarded); args are passed through.
func TestResolveCommand(t *testing.T) {
	m := &Conversation{commands: []Command{
		{Name: "skills", Help: "list skills", Run: func(string) string { return "SKILL OUTPUT" }},
		{Name: "doctor", Help: "health", Run: func(string) string { return "DOCTOR OK" }},
		{Name: "echo", Help: "echo args", Run: func(a string) string { return "got:" + a }},
	}}

	if got := m.resolveCommand("/skills"); got != "SKILL OUTPUT" {
		t.Errorf("/skills routed wrong: %q", got)
	}
	if got := m.resolveCommand("/echo hello world"); got != "got:hello world" {
		t.Errorf("args not passed through: %q", got)
	}
	if got := m.resolveCommand("/bogus"); !strings.Contains(got, "unknown command") {
		t.Errorf("/bogus should be reported: %q", got)
	}
	help := m.resolveCommand("/help")
	for _, want := range []string{"/help", "/skills", "/doctor", "/echo", "sent to the conductor"} {
		if !strings.Contains(help, want) {
			t.Errorf("/help missing %q:\n%s", want, help)
		}
	}
	// Bare "/" is treated as help.
	if m.resolveCommand("/") != help {
		t.Error("bare / should render help")
	}
}

// TestResolveAsyncCommand (or-xe7.7): a command with an Async handler dispatches via
// resolveAsyncCommand — returning its immediate line + a follow-up tea.Cmd that yields a
// CommandResultMsg; a sync command is not treated as async.
func TestResolveAsyncCommand(t *testing.T) {
	ran := false
	m := &Conversation{commands: []Command{
		{Name: "skills", Help: "sync", Run: func(string) string { return "SYNC" }},
		{Name: "mcp", Help: "async", Async: func(a string) (string, tea.Cmd) {
			return "working:" + a, func() tea.Msg { ran = true; return CommandResultMsg{Text: "done"} }
		}},
	}}

	immediate, cmd, ok := m.resolveAsyncCommand("/mcp login")
	if !ok || cmd == nil {
		t.Fatalf("async command should dispatch: ok=%v cmd=%v", ok, cmd)
	}
	if immediate != "working:login" {
		t.Errorf("immediate line = %q", immediate)
	}
	if res := cmd(); res != (CommandResultMsg{Text: "done"}) || !ran {
		t.Errorf("follow-up cmd should yield CommandResultMsg{done}: %v ran=%v", res, ran)
	}
	if _, _, ok := m.resolveAsyncCommand("/skills"); ok {
		t.Error("a sync command must not be dispatched as async")
	}
}

// TestCommandPalette (arrow-key command navigation): the palette is open only for a bare /command
// prefix, filters by that prefix, and the selection index clamps into range.
func TestCommandPalette(t *testing.T) {
	m := &Conversation{commands: []Command{{Name: "status"}, {Name: "doctor"}, {Name: "mcp"}}}
	m.input = textarea.New()

	m.input.SetValue("hello") // not a slash-command → closed
	if got := m.paletteMatches(); len(got) != 0 {
		t.Errorf("non-slash input should close the palette, got %d", len(got))
	}
	m.input.SetValue("/mcp set x") // args typed (a space) → closed
	if got := m.paletteMatches(); len(got) != 0 {
		t.Errorf("typing args should close the palette, got %d", len(got))
	}
	m.input.SetValue("/d") // prefix filter
	if got := m.paletteMatches(); len(got) != 1 || got[0].Name != "doctor" {
		t.Errorf("/d should match only doctor, got %+v", got)
	}
	m.input.SetValue("/") // bare slash → injected commands + built-in intrinsics
	// status/doctor/mcp (injected) + help/clear/compact/context/model/exit (intrinsic) = 9
	if got := m.paletteMatches(); len(got) != 9 {
		t.Errorf("/ should list the 3 injected + 6 intrinsic commands, got %d", len(got))
	}

	m.paletteIdx = 99
	if m.clampPalette(3) != 2 {
		t.Error("index above range should clamp to last")
	}
	m.paletteIdx = -5
	if m.clampPalette(3) != 0 {
		t.Error("index below range should clamp to first")
	}
}

// TestInputHistoryRecall (or-d38): ↑/↓ recall previously-submitted lines shell-style, stashing the
// unsent draft at the live line and collapsing consecutive duplicates.
func TestInputHistoryRecall(t *testing.T) {
	m := &Conversation{}
	m.input = textarea.New()

	m.historyPrev() // empty history → no-op
	if m.input.Value() != "" {
		t.Errorf("empty-history ↑ should no-op, got %q", m.input.Value())
	}

	m.recordHistory("/status")
	m.recordHistory("build a time service")

	m.input.SetValue("draft") // an unsent line at the live position
	m.historyPrev()
	if m.input.Value() != "build a time service" {
		t.Errorf("↑ #1 should recall the newest, got %q", m.input.Value())
	}
	m.historyPrev()
	if m.input.Value() != "/status" {
		t.Errorf("↑ #2 should recall the older, got %q", m.input.Value())
	}
	m.historyPrev()
	if m.input.Value() != "/status" {
		t.Errorf("↑ past the oldest should stay, got %q", m.input.Value())
	}

	m.historyNext()
	if m.input.Value() != "build a time service" {
		t.Errorf("↓ #1 should move to the newer, got %q", m.input.Value())
	}
	m.historyNext()
	if m.input.Value() != "draft" {
		t.Errorf("↓ back to the live line should restore the draft, got %q", m.input.Value())
	}

	m.recordHistory("build a time service") // consecutive duplicate → collapsed
	if len(m.history) != 2 {
		t.Errorf("consecutive duplicate should collapse, history=%v", m.history)
	}
}
