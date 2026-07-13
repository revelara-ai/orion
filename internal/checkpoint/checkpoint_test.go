package checkpoint

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	wt := t.TempDir()
	shadow := filepath.Join(t.TempDir(), "shadow.git") // OUTSIDE the worktree
	s, err := New(wt, shadow)
	if err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	return s, wt
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, rel string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// or-ykz.15 DONE-WHEN: a mutating turn followed by rollback restores the EXACT
// pre-turn tree — the edit reverted AND the turn-added file removed.
func TestCheckpointRollbackRestoresExactTree(t *testing.T) {
	s, wt := newStore(t)
	ctx := context.Background()

	write(t, wt, "keep.go", "package t\n\nfunc Keep() int { return 1 }\n")
	write(t, wt, "sub/data.txt", "original\n")
	if err := s.Checkpoint(ctx, "task-1"); err != nil {
		t.Fatal(err)
	}

	// A bad turn: mutate a tracked file, delete another, add a new one.
	write(t, wt, "keep.go", "package t\n\nfunc Keep() int { return 999 /* broke it */ }\n")
	if err := os.Remove(filepath.Join(wt, "sub/data.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, wt, "turn-added.go", "package t\n\n// junk from a bad turn\n")

	if err := s.Rollback(ctx, "task-1"); err != nil {
		t.Fatal(err)
	}

	// Exact pre-turn tree: the mutation reverted…
	if got, _ := read(t, wt, "keep.go"); got != "package t\n\nfunc Keep() int { return 1 }\n" {
		t.Fatalf("tracked mutation not reverted: %q", got)
	}
	// …the deleted file restored…
	if got, ok := read(t, wt, "sub/data.txt"); !ok || got != "original\n" {
		t.Fatalf("deleted file not restored: %q ok=%v", got, ok)
	}
	// …and the turn-added file removed.
	if _, ok := read(t, wt, "turn-added.go"); ok {
		t.Fatal("turn-added file must be removed on rollback (exact-tree)")
	}
}

// Multiple checkpoints dedup + each rolls back to its own point.
func TestCheckpointsAreIndependent(t *testing.T) {
	s, wt := newStore(t)
	ctx := context.Background()

	write(t, wt, "f.txt", "v1\n")
	if err := s.Checkpoint(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	write(t, wt, "f.txt", "v2\n")
	if err := s.Checkpoint(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	write(t, wt, "f.txt", "v3-uncommitted\n")

	if err := s.Rollback(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if got, _ := read(t, wt, "f.txt"); got != "v1\n" {
		t.Fatalf("rollback to a: got %q", got)
	}
	if err := s.Rollback(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if got, _ := read(t, wt, "f.txt"); got != "v2\n" {
		t.Fatalf("rollback to b: got %q", got)
	}
}

// Guardrails: an invalid id is refused; rollback to a missing checkpoint errors.
func TestCheckpointGuardrails(t *testing.T) {
	s, wt := newStore(t)
	ctx := context.Background()
	write(t, wt, "x.txt", "x\n")

	if err := s.Checkpoint(ctx, "bad id with spaces"); err == nil {
		t.Fatal("an invalid ref name must be refused")
	}
	if err := s.Checkpoint(ctx, "../escape"); err == nil {
		t.Fatal("a traversal id must be refused")
	}
	if err := s.Rollback(ctx, "never-created"); err == nil {
		t.Fatal("rollback to a missing checkpoint must error")
	}
	if s.Exists(ctx, "never-created") {
		t.Fatal("Exists must be false for a missing checkpoint")
	}
}
