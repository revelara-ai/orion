package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkillDir(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func md(name, desc string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\nbody for " + name + "\n"
}

// TestLoadDirDiscoversSkills (or-96z): each subdirectory with a SKILL.md is discovered; a plain
// file (README) at the root is ignored.
func TestLoadDirDiscoversSkills(t *testing.T) {
	root := t.TempDir()
	writeSkillDir(t, root, "pdf-tool", md("pdf-tool", "handle pdfs"))
	writeSkillDir(t, root, "data-tool", md("data-tool", "analyze data"))
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("not a skill"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := New()
	n, err := r.LoadDir(root, TrustGeneration)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 skills discovered, got %d (warnings: %v)", n, r.Warnings())
	}
	if _, ok := r.Get("pdf-tool"); !ok {
		t.Fatal("pdf-tool not registered")
	}
	if got := len(r.List()); got != 2 {
		t.Fatalf("List should return 2, got %d", got)
	}
}

// TestLoadDirSkipsSymlinkedSkill: a symlinked SKILL.md is not followed (it could point outside
// the scan root), and a diagnostic is recorded.
func TestLoadDirSkipsSymlinkedSkill(t *testing.T) {
	root := t.TempDir()
	// A real skill, plus a directory whose SKILL.md is a symlink to a file outside the root.
	writeSkillDir(t, root, "real", md("real", "a real skill"))
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("---\nname: evil\ndescription: leaked\n---\nx"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "sneaky")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(linkDir, "SKILL.md")); err != nil {
		t.Skipf("symlinks unsupported here: %v", err)
	}

	r := New()
	n, err := r.LoadDir(root, TrustGeneration)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("only the real skill should load (symlink skipped), got %d", n)
	}
	if _, ok := r.Get("evil"); ok {
		t.Fatal("a symlinked SKILL.md was followed — escape not prevented")
	}
	found := false
	for _, w := range r.Warnings() {
		if strings.Contains(w, "symlink") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a symlink-skip warning: %v", r.Warnings())
	}
}

// TestTrustIsScopeAssigned: trust comes from the LoadDir scope, and proof skills are not
// reloadable (invariant 8) while generation skills are.
func TestTrustIsScopeAssigned(t *testing.T) {
	proofRoot := t.TempDir()
	writeSkillDir(t, proofRoot, "builtin", md("builtin", "a built-in skill"))
	r := New()
	if _, err := r.LoadDir(proofRoot, TrustProof); err != nil {
		t.Fatal(err)
	}
	s, _ := r.Get("builtin")
	if s.Trust != TrustProof {
		t.Fatalf("expected scope-assigned proof trust, got %q", s.Trust)
	}
	if s.Trust.Reloadable() {
		t.Fatal("a proof skill must NOT be reloadable (invariant 8)")
	}
	if !TrustGeneration.Reloadable() {
		t.Fatal("a generation skill must be reloadable")
	}
}

// TestCollisionPrecedence: a later LoadDir overrides an earlier one (call user/built-in first,
// project last → project wins), and a shadowing warning is recorded.
func TestCollisionPrecedence(t *testing.T) {
	userRoot, projRoot := t.TempDir(), t.TempDir()
	writeSkillDir(t, userRoot, "dup", md("dup", "user version"))
	writeSkillDir(t, projRoot, "dup", md("dup", "project version"))

	r := New()
	_, _ = r.LoadDir(userRoot, TrustProof)      // user/built-in scope first
	_, _ = r.LoadDir(projRoot, TrustGeneration) // project scope last → wins
	s, _ := r.Get("dup")
	if s.Description != "project version" || s.Trust != TrustGeneration {
		t.Fatalf("project scope should win the collision, got desc=%q trust=%q", s.Description, s.Trust)
	}
	found := false
	for _, w := range r.Warnings() {
		if strings.Contains(w, "shadow") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a shadowing warning should be recorded: %v", r.Warnings())
	}
}

// TestCatalog: the tier-1 catalog carries name + description but not the body.
func TestCatalog(t *testing.T) {
	root := t.TempDir()
	writeSkillDir(t, root, "pdf-tool", md("pdf-tool", "handle pdfs"))
	r := New()
	if _, err := r.LoadDir(root, TrustGeneration); err != nil {
		t.Fatal(err)
	}
	cat := r.Catalog()
	if !strings.Contains(cat, "pdf-tool") || !strings.Contains(cat, "handle pdfs") {
		t.Fatalf("catalog missing name/description: %q", cat)
	}
	if strings.Contains(cat, "body for pdf-tool") {
		t.Fatalf("catalog must NOT include the body (progressive disclosure): %q", cat)
	}
	if New().Catalog() != "" {
		t.Fatal("an empty registry should render an empty catalog")
	}
}
