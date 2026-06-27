package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadAgentsDiscoversFromHome (or-oy3): a subagent .md dropped in ~/.claude/agents is
// discovered by loadAgents via the default scopes (the ecosystem interop path).
func TestLoadAgentsDiscoversFromHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reviewer.md"),
		[]byte("---\nname: reviewer\ndescription: reviews code\ntools: Read, Grep\n---\nYou review code.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := loadAgents()
	if err != nil {
		t.Fatal(err)
	}
	a, ok := r.Get("reviewer")
	if !ok {
		t.Fatalf("reviewer not discovered from ~/.claude/agents; have %d", len(r.List()))
	}
	if len(a.Tools) != 2 || !a.Trust.Reloadable() {
		t.Fatalf("unexpected discovered agent: %+v", a)
	}
}
