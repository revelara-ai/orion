package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadSkillsDiscoversFromHome (or-alp): a skill dropped into ~/.agents/skills (the
// cross-client convention) is discovered by loadSkills via the default scopes — the OSS-interop
// path a Claude Code / Cursor / Codex skill travels.
func TestLoadSkillsDiscoversFromHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".agents", "skills", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: demo\ndescription: a demo skill\n---\ndo the demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := loadSkills()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := r.Get("demo")
	if !ok {
		t.Fatalf("skill 'demo' not discovered from ~/.agents/skills; have %d skills", len(r.List()))
	}
	if s.Description != "a demo skill" || !s.Trust.Reloadable() {
		t.Fatalf("unexpected discovered skill: %+v", s)
	}
}
