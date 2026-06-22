package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

func mustStore(t *testing.T) *contextstore.Store {
	t.Helper()
	s, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestResolveInitsManagedRepoForGreenfield(t *testing.T) {
	store := mustStore(t)
	ctx := context.Background()

	r, err := Resolve(ctx, store, Intake{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	wantPath := filepath.Join(store.Dir(), "repo")
	if r.Path != wantPath {
		t.Fatalf("Path = %q, want %q", r.Path, wantPath)
	}
	if r.Base != "main" {
		t.Fatalf("Base = %q, want main", r.Base)
	}
	// It is a real repo on main with one commit.
	if br := gitOut(t, r.Path, "rev-parse", "--abbrev-ref", "HEAD"); br != "main" {
		t.Fatalf("HEAD branch = %q, want main", br)
	}
	count := gitOut(t, r.Path, "rev-list", "--count", "HEAD")
	if count != "1" {
		t.Fatalf("commit count = %q, want 1 (the init commit)", count)
	}

	// Idempotent: re-resolve reuses the repo, adds no second commit.
	r2, err := Resolve(ctx, store, Intake{})
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	if r2.Path != r.Path {
		t.Fatalf("re-resolve Path = %q, want %q", r2.Path, r.Path)
	}
	if c := gitOut(t, r.Path, "rev-list", "--count", "HEAD"); c != "1" {
		t.Fatalf("commit count after re-resolve = %q, want still 1 (idempotent)", c)
	}
	if _, err := os.Stat(filepath.Join(r.Path, ".git")); err != nil {
		t.Fatalf("managed repo .git should exist: %v", err)
	}
}

// newSourceRepo creates a throwaway upstream repo on `trunk` with one commit,
// returning its path — the brownfield clone target.
func newSourceRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitOut(t, dir, "init", "-b", "trunk")
	gitOut(t, dir, "config", "user.email", "src@example.com")
	gitOut(t, dir, "config", "user.name", "Src")
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "add", ".")
	gitOut(t, dir, "commit", "-m", "upstream")
	return dir
}

func TestResolveClonesBrownfieldTarget(t *testing.T) {
	store := mustStore(t)
	ctx := context.Background()
	src := newSourceRepo(t)

	r, err := Resolve(ctx, store, Intake{Brownfield: true, Source: src})
	if err != nil {
		t.Fatalf("resolve brownfield: %v", err)
	}
	if r.Path != filepath.Join(store.Dir(), "repo") {
		t.Fatalf("Path = %q", r.Path)
	}
	// Base is the cloned upstream default branch, not a hardcoded "main".
	if r.Base != "trunk" {
		t.Fatalf("Base = %q, want trunk (the cloned default branch)", r.Base)
	}
	// The upstream content came across.
	if _, err := os.Stat(filepath.Join(r.Path, "app.go")); err != nil {
		t.Fatalf("cloned content missing: %v", err)
	}
}
