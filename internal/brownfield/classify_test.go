package brownfield

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/proof/safeenv"
)

// TestClassifyGreenfield: an empty (or config-only) directory with no source and no
// git history reads as greenfield — Orion will create new structure.
func TestClassifyGreenfield(t *testing.T) {
	dir := t.TempDir()
	// config-only, no source
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# new"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Classify(dir)
	if p.Mode != Greenfield {
		t.Fatalf("empty/config-only dir should be greenfield: %+v", p)
	}
	if p.SourceFiles != 0 {
		t.Fatalf("no source expected: %+v", p)
	}
}

// TestClassifyBrownfieldBySource: a directory with existing source reads as
// brownfield — Orion will integrate with it.
func TestClassifyBrownfieldBySource(t *testing.T) {
	dir := t.TempDir()
	must(t, filepath.Join(dir, "lib.go"), "package x\nfunc F() int { return 1 }\n")
	must(t, filepath.Join(dir, "lib_test.go"), "package x\n")
	p := Classify(dir)
	if p.Mode != Brownfield {
		t.Fatalf("a dir with source should be brownfield: %+v", p)
	}
	if p.SourceFiles != 2 || !p.HasTests {
		t.Fatalf("expected 2 go source files incl. a test: %+v", p)
	}
	if len(p.Languages) != 1 || p.Languages[0] != "go" {
		t.Fatalf("expected go language: %+v", p)
	}
}

// TestClassifyIgnoresOrionBuildOutput: a fresh repo that has only run an Orion build
// (so it has an orion-build/ dir) still reads as greenfield — the build output is
// not the developer's existing code.
func TestClassifyIgnoresOrionBuildOutput(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "orion-build", "time-service")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	must(t, filepath.Join(out, "main.go"), "package main\nfunc main() {}\n")
	p := Classify(dir)
	if p.Mode != Greenfield {
		t.Fatalf("orion-build output must not flip a fresh repo to brownfield: %+v", p)
	}
}

// TestClassifyBrownfieldByGitHistory: a git repo with commits reads as brownfield even
// before any source is detected at the top level (an existing project).
func TestClassifyBrownfieldByGitHistory(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(safeenv.Build(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}
	git("init")
	must(t, filepath.Join(dir, "notes.txt"), "history")
	git("add", "-A")
	git("commit", "-m", "initial", "--no-verify")

	p := Classify(dir)
	if !p.HasGit || !p.HasCommits {
		t.Fatalf("git history not detected: %+v", p)
	}
	if p.Mode != Brownfield {
		t.Fatalf("a repo with commit history should be brownfield: %+v", p)
	}
}
