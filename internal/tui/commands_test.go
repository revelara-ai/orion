package tui

import (
	"strings"
	"testing"

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
