package repo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

func targetStore(t *testing.T) *contextstore.Store {
	t.Helper()
	st, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestResolveGreenfieldTarget (or-045a.7 DONE-WHEN a): a ratified target path
// gets its own repo initialized THERE (git init -b main), not under the
// store — and resolving again reuses it unchanged.
func TestResolveGreenfieldTarget(t *testing.T) {
	st := targetStore(t)
	target := filepath.Join(t.TempDir(), "mech-pve-game")
	r, err := Resolve(context.Background(), st, Intake{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if r.Path != target {
		t.Fatalf("the repo must live at the ratified target: %q", r.Path)
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
		t.Fatalf("the target must be a real git repo: %v", err)
	}
	if r.Base != "main" {
		t.Fatalf("greenfield target base = %q, want main", r.Base)
	}
	// Idempotent: a second resolve reuses it.
	r2, err := Resolve(context.Background(), st, Intake{Target: target})
	if err != nil || r2.Path != target {
		t.Fatalf("re-resolve must reuse the target: %+v err=%v", r2, err)
	}
	// Negative: the default path is untouched (nothing under <store>/repo).
	if _, err := os.Stat(filepath.Join(st.Dir(), "repo")); !os.IsNotExist(err) {
		t.Fatal("a targeted greenfield must not also create the default managed repo")
	}
}

// TestResolveTargetValidation (or-045a.7): a target inside the store or inside
// the harness cwd is REFUSED — that is exactly how mech-pve-game ended up
// scaffolded into the Orion repo.
func TestResolveTargetValidation(t *testing.T) {
	st := targetStore(t)
	ctx := context.Background()

	// Inside the store dir: refused.
	if _, err := Resolve(ctx, st, Intake{Target: filepath.Join(st.Dir(), "nested")}); err == nil || !strings.Contains(err.Error(), "store") {
		t.Fatalf("a target inside the store must be refused, got: %v", err)
	}
	// Inside the current working directory (the harness repo when dogfooding): refused.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(ctx, st, Intake{Target: filepath.Join(cwd, "scaffold-here")}); err == nil || !strings.Contains(err.Error(), "working directory") {
		t.Fatalf("a target inside the harness cwd must be refused, got: %v", err)
	}
	// A brownfield intake never takes a target (mutually exclusive shapes).
	if _, err := Resolve(ctx, st, Intake{Brownfield: true, Source: "/x", Target: "/y"}); err == nil {
		t.Fatal("brownfield + target must be refused")
	}
	// Negative: no target keeps the default managed-repo path (V2 unchanged).
	r, err := Resolve(ctx, st, Intake{})
	if err != nil || r.Path != filepath.Join(st.Dir(), "repo") {
		t.Fatalf("no target must default to <store>/repo: %+v err=%v", r, err)
	}
}
