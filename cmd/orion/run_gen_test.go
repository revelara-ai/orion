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

// TestResolveAgentChain (or-ykz.13): ORION_AGENT accepts an ordered
// comma-separated failover chain; unknown/off-PATH entries are skipped; a
// single name behaves exactly as before; empty/fixture resolves to none.
func TestResolveAgentChain(t *testing.T) {
	onPath := func(string) (string, error) { return "/usr/bin/x", nil }
	chain, ok := resolveAgentChain("claude,nonexistent-agent,gemini", onPath)
	if !ok || len(chain) != 2 || chain[0].Name != "claude" || chain[1].Name != "gemini" {
		t.Fatalf("chain must keep order and skip unknowns: %+v ok=%v", chain, ok)
	}
	if single, ok := resolveAgentChain("claude", onPath); !ok || len(single) != 1 {
		t.Fatalf("a single name must resolve as a one-entry chain: %+v", single)
	}
	if _, ok := resolveAgentChain("", onPath); ok {
		t.Fatal("empty must resolve to none (fixture path)")
	}
	if _, ok := resolveAgentChain("fixture", onPath); ok {
		t.Fatal("fixture must resolve to none")
	}
}
