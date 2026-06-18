package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out)
	}
}

// newRepo creates a main repo on `main` with one commit, returns its path.
func newRepo(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	repo := filepath.Join(base, "myrepo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "init", "-b", "main")
	run(t, repo, "git", "config", "user.email", "t@example.com")
	run(t, repo, "git", "config", "user.name", "Tester")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-m", "init")
	return repo
}

func mustStore(t *testing.T) *contextstore.Store {
	t.Helper()
	s, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestCreateOffMainSharedObjectStore: Create makes worktrees-<repo>/<issue-id>
// on branch <issue-id> off main, sharing the object store (no re-clone → .git is
// a file pointer, not a directory).
func TestCreateOffMainSharedObjectStore(t *testing.T) {
	repo := newRepo(t)
	m := New(repo, mustStore(t))
	ctx := context.Background()

	wt, err := m.Create(ctx, "or-9xl", "main")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	wantPath := filepath.Join(filepath.Dir(repo), "worktrees-myrepo", "or-9xl")
	if wt.Path != wantPath {
		t.Fatalf("path = %q, want %q", wt.Path, wantPath)
	}
	if wt.Branch != "or-9xl" {
		t.Fatalf("branch = %q, want or-9xl", wt.Branch)
	}
	if fi, err := os.Stat(wt.Path); err != nil || !fi.IsDir() {
		t.Fatalf("worktree dir missing: %v", err)
	}
	// Shared object store: a worktree's .git is a FILE (gitdir pointer), not a clone.
	gitPath := filepath.Join(wt.Path, ".git")
	fi, err := os.Stat(gitPath)
	if err != nil {
		t.Fatalf("worktree .git missing: %v", err)
	}
	if fi.IsDir() {
		t.Fatal(".git is a directory — looks like a clone, not a shared-object worktree")
	}
	// `git -C <repo> worktree list` shows the entry.
	out, _ := m.git("worktree", "list")
	if !strings.Contains(out, wt.Path) {
		t.Fatalf("worktree list does not show %s:\n%s", wt.Path, out)
	}
}

// TestRemoveRefusesUnmergedWorkWithoutForce: a dirty worktree is not deleted
// without --force, but is with it; afterward git no longer lists it.
func TestRemoveRefusesUnmergedWorkWithoutForce(t *testing.T) {
	repo := newRepo(t)
	m := New(repo, mustStore(t))
	ctx := context.Background()

	wt, err := m.Create(ctx, "or-evb", "main")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Introduce uncommitted work.
	if err := os.WriteFile(filepath.Join(wt.Path, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.Remove(ctx, "or-evb", RemoveOpts{Force: false}); err == nil {
		t.Fatal("expected Remove to refuse a dirty worktree without --force")
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("worktree should still exist after refused remove: %v", err)
	}

	if err := m.Remove(ctx, "or-evb", RemoveOpts{Force: true}); err != nil {
		t.Fatalf("forced remove failed: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree dir should be gone after forced remove")
	}
	out, _ := m.git("worktree", "list")
	if strings.Contains(out, wt.Path) {
		t.Fatalf("worktree still listed after remove:\n%s", out)
	}
}

// TestRemoveRefusesMidIntegrationEvenForce: a task mid-integration is never
// removed, even with --force.
func TestRemoveRefusesMidIntegrationEvenForce(t *testing.T) {
	repo := newRepo(t)
	m := New(repo, mustStore(t)).WithIntegrationCheck(func(id string) bool { return id == "or-60u" })
	ctx := context.Background()

	wt, err := m.Create(ctx, "or-60u", "main")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := m.Remove(ctx, "or-60u", RemoveOpts{Force: true}); err == nil {
		t.Fatal("expected Remove to refuse a mid-integration worktree even with --force")
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("worktree should still exist: %v", err)
	}
}

// TestReconcilePrunesDeletedAndReapsStale: a manually-deleted worktree is pruned
// and its record marked gone; an orphan/incomplete dir is reaped.
func TestReconcilePrunesDeletedAndReapsStale(t *testing.T) {
	repo := newRepo(t)
	store := mustStore(t)
	m := New(repo, store)
	ctx := context.Background()

	wt, err := m.Create(ctx, "or-xgj", "main")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Manually delete the worktree dir (simulating an out-of-band deletion).
	if err := os.RemoveAll(wt.Path); err != nil {
		t.Fatal(err)
	}
	// Drop an orphan/incomplete dir with no .git.
	orphan := m.PathFor("or-stale")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := m.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Deleted worktree is pruned out of git's list.
	out, _ := m.git("worktree", "list")
	if strings.Contains(out, wt.Path) {
		t.Fatalf("deleted worktree still listed after reconcile:\n%s", out)
	}
	// Orphan dir reaped.
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan dir should be reaped")
	}
	// Store record repaired to gone.
	var status string
	_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
		rec, e := tx.Worktrees().Get(ctx, "or-xgj")
		if e == nil {
			status = rec.Status
		}
		return nil
	})
	if status != "gone" {
		t.Fatalf("record status = %q, want gone", status)
	}
}

// TestCreateRecordsWorktreeTxn: Create persists path+branch to the worktrees
// entity so a crash leaves a recoverable record.
func TestCreateRecordsWorktreeTxn(t *testing.T) {
	repo := newRepo(t)
	store := mustStore(t)
	m := New(repo, store)
	ctx := context.Background()

	wt, err := m.Create(ctx, "or-0d2", "main")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var rec contextstore.WorktreeRecord
	if err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		rec, e = tx.Worktrees().Get(ctx, "or-0d2")
		return e
	}); err != nil {
		t.Fatalf("get worktree record: %v", err)
	}
	if rec.Path != wt.Path || rec.Branch != "or-0d2" || rec.Status != "active" {
		t.Fatalf("record = %+v, want path=%s branch=or-0d2 status=active", rec, wt.Path)
	}
}
