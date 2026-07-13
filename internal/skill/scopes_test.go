package skill

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaultScopesOrder: user scopes precede project scopes (project wins collisions), and the
// conventional subpaths (.agents/.claude/.orion skills) are present.
func TestDefaultScopesOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	proj := t.TempDir()

	scopes := DefaultScopes(proj)
	if len(scopes) != 8 {
		t.Fatalf("expected 4 user + 4 project scopes (.agents/.claude/.codex/.orion), got %d", len(scopes))
	}
	// First four are under home, last four under project.
	for i, s := range scopes {
		wantBase := home
		if i >= 4 {
			wantBase = proj
		}
		if !strings.HasPrefix(s.Root, wantBase) {
			t.Errorf("scope %d (%s) not under %s", i, s.Root, wantBase)
		}
		if s.Trust != TrustGeneration {
			t.Errorf("scope %d should be generation-trust, got %s", i, s.Trust)
		}
	}
	if filepath.Base(filepath.Dir(scopes[0].Root)) != ".agents" {
		t.Errorf("first scope should be .agents/skills, got %s", scopes[0].Root)
	}
}

// TestLoadScopes: scopes load in order; a project skill (later) overrides a same-named user
// skill (earlier).
func TestLoadScopes(t *testing.T) {
	userRoot, projRoot := t.TempDir(), t.TempDir()
	writeSkillDir(t, userRoot, "dup", md("dup", "user version"))
	writeSkillDir(t, projRoot, "dup", md("dup", "project version"))
	writeSkillDir(t, userRoot, "only-user", md("only-user", "just user"))

	r := New()
	if err := r.LoadScopes([]Scope{{Root: userRoot, Trust: TrustGeneration}, {Root: projRoot, Trust: TrustGeneration}}); err != nil {
		t.Fatal(err)
	}
	if len(r.List()) != 2 {
		t.Fatalf("expected 2 distinct skills, got %d", len(r.List()))
	}
	if s, _ := r.Get("dup"); s.Description != "project version" {
		t.Fatalf("project scope should win, got %q", s.Description)
	}
}
