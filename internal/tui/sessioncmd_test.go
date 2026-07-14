package tui

import (
	"strings"
	"testing"
)

// TestSessionsResumeCommandsRouted (or-8my7): /sessions and /resume are known
// commands (listed in /help) and route to the conductor as control ops — never
// reported as "unknown command /resume".
func TestSessionsResumeCommandsRouted(t *testing.T) {
	m := newTestConvo(t)

	help := m.commandHelp()
	for _, name := range []string{"sessions", "resume"} {
		if !strings.Contains(help, "/"+name) {
			t.Errorf("/help must list /%s:\n%s", name, help)
		}
	}

	for _, line := range []string{"/sessions", "/resume 20260101T000000_x"} {
		cmd := m.handleCommand(line)
		if cmd == nil {
			t.Fatalf("%q must dispatch a control command, not report 'unknown command'", line)
		}
		if res, ok := cmd().(CommandResultMsg); !ok || !strings.Contains(res.Text, "not connected") {
			t.Fatalf("%q with no client must degrade to a control result, got %#v", line, cmd())
		}
		for _, mm := range m.msgs {
			if strings.Contains(mm.text, "unknown command") {
				t.Fatalf("%q must not be reported unknown: %q", line, mm.text)
			}
		}
	}
}
