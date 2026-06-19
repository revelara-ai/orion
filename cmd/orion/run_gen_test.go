package main

import (
	"errors"
	"testing"
)

// TestResolveAgentSelection: generation defaults to the deterministic fixture and
// only spawns a vendor agent when ORION_AGENT names a known preset whose binary is
// present — so `orion run` never silently spawns an agent or uses quota.
func TestResolveAgentSelection(t *testing.T) {
	absent := func(string) (string, error) { return "", errors.New("not found") }
	present := func(string) (string, error) { return "/usr/bin/agent", nil }

	cases := []struct {
		name     string
		envName  string
		lookPath func(string) (string, error)
		wantUse  bool
	}{
		{"unset → fixture", "", present, false},
		{"none → fixture", "none", present, false},
		{"fixture → fixture", "fixture", present, false},
		{"unknown preset → fixture", "definitely-not-real", present, false},
		{"known but binary absent → fixture", "claude", absent, false},
		{"known + binary present → agent", "claude", present, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, ok := resolveAgent(c.envName, c.lookPath)
			if ok != c.wantUse {
				t.Fatalf("resolveAgent(%q) ok=%v, want %v", c.envName, ok, c.wantUse)
			}
			if ok && p.Command == "" {
				t.Fatal("selected agent has no launch command")
			}
		})
	}
}
