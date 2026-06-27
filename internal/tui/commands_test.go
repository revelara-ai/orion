package tui

import (
	"strings"
	"testing"
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
