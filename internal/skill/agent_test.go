package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAgentCommaTools(t *testing.T) {
	a, _, err := ParseAgent([]byte("---\nname: reviewer\ndescription: reviews code\ntools: Read, Grep, Bash\nmodel: opus\n---\nYou are a code reviewer.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "reviewer" || a.Model != "opus" {
		t.Fatalf("frontmatter not parsed: %+v", a)
	}
	if len(a.Tools) != 3 || a.Tools[0] != "Read" || a.Tools[2] != "Bash" {
		t.Fatalf("comma-separated tools not parsed: %v", a.Tools)
	}
	if a.Body != "You are a code reviewer." {
		t.Fatalf("body (system prompt) not isolated: %q", a.Body)
	}
}

func TestParseAgentListTools(t *testing.T) {
	a, _, err := ParseAgent([]byte("---\nname: r\ndescription: d\ntools:\n  - Read\n  - Edit\n---\nbody"))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Tools) != 2 || a.Tools[1] != "Edit" {
		t.Fatalf("YAML-list tools not parsed: %v", a.Tools)
	}
}

func TestParseAgentOmittedToolsInheritAll(t *testing.T) {
	a, _, err := ParseAgent([]byte("---\nname: r\ndescription: d\n---\nbody"))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Tools) != 0 {
		t.Fatalf("omitted tools should be empty (inherit all): %v", a.Tools)
	}
}

func TestParseAgentRequiresDescription(t *testing.T) {
	if _, _, err := ParseAgent([]byte("---\nname: r\n---\nbody")); err == nil {
		t.Fatal("a missing description must be fatal")
	}
}

func writeAgentFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAgentRegistryDiscover(t *testing.T) {
	dir := t.TempDir()
	writeAgentFile(t, dir, "reviewer.md", "---\nname: reviewer\ndescription: reviews\n---\nrole")
	writeAgentFile(t, dir, "planner.md", "---\nname: planner\ndescription: plans\n---\nrole")
	writeAgentFile(t, dir, "notes.md", "no frontmatter here") // warns, not loaded

	r := NewAgentRegistry()
	n, err := r.LoadDir(dir, TrustGeneration)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 agents, got %d (warnings: %v)", n, r.Warnings())
	}
	if _, ok := r.Get("reviewer"); !ok {
		t.Fatal("reviewer not registered")
	}
	if !strings.Contains(r.Catalog(), "planner") {
		t.Fatalf("catalog missing planner: %q", r.Catalog())
	}
}

func TestAgentProofNotShadowed(t *testing.T) {
	proofDir, genDir := t.TempDir(), t.TempDir()
	writeAgentFile(t, proofDir, "x.md", "---\nname: x\ndescription: curated\n---\nr")
	writeAgentFile(t, genDir, "x.md", "---\nname: x\ndescription: impostor\n---\nr")
	r := NewAgentRegistry()
	_, _ = r.LoadDir(proofDir, TrustProof)
	_, _ = r.LoadDir(genDir, TrustGeneration)
	a, _ := r.Get("x")
	if a.Description != "curated" || a.Trust != TrustProof {
		t.Fatalf("a generation agent must not shadow a proof agent: %q (%s)", a.Description, a.Trust)
	}
}

func TestDiscoverAgentsDocs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Project agent instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewAgentRegistry()
	r.DiscoverAgentsDocs(dir)
	if len(r.Docs()) != 1 || !strings.Contains(r.Docs()[0].Content, "agent instructions") {
		t.Fatalf("AGENTS.md not discovered as a freeform doc: %+v", r.Docs())
	}
}
