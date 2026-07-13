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

// TestRecreateReplacesStaleWorktreeAndBranch (or-d3w): Recreate yields a FRESH tree even when a
// prior worktree + branch of the same name exist — the case that broke `orion run` re-runs, where
// the epic-integration head collided on the leftover branch from a previous run.
func TestRecreateReplacesStaleWorktreeAndBranch(t *testing.T) {
	repo := newRepo(t)
	m := New(repo, mustStore(t))
	ctx := context.Background()

	wt1, err := m.Create(ctx, "epic-integration", "main")
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	// A stale integration head carrying an un-integrated commit (a prior run's assembly).
	if err := os.WriteFile(filepath.Join(wt1.Path, "stale.txt"), []byte("prior run"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, wt1.Path, "git", "add", "-A")
	run(t, wt1.Path, "git", "commit", "-m", "stale head")

	// Precondition (the bug): a plain Create now collides on the existing branch.
	if _, err := m.Create(ctx, "epic-integration", "main"); err == nil {
		t.Fatal("precondition: a second Create must collide on the existing epic-integration branch")
	}

	// Recreate succeeds and yields a FRESH tree from main (the stale commit is gone).
	wt2, err := m.Recreate(ctx, "epic-integration", "main")
	if err != nil {
		t.Fatalf("recreate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt2.Path, "stale.txt")); !os.IsNotExist(err) {
		t.Errorf("recreated head must be fresh from main (no stale.txt); stat err = %v", err)
	}
	if out, _ := m.git("worktree", "list"); !strings.Contains(out, wt2.Path) {
		t.Errorf("worktree list should show the recreated head:\n%s", out)
	}
}

// TestRecreateSurvivesGhostRegistration (or-d3w): if the worktree DIR was removed out-of-band
// (leaving a prunable git registration + the branch — e.g. a WSL2 rm -rf or a crash before prune),
// Recreate must still succeed. It prunes the ghost first, so `git branch -D` no longer sees the
// branch as "used by worktree" and the fresh `worktree add -b` no longer collides.
func TestRecreateSurvivesGhostRegistration(t *testing.T) {
	repo := newRepo(t)
	m := New(repo, mustStore(t))
	ctx := context.Background()

	wt, err := m.Create(ctx, "epic-integration", "main")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Out-of-band deletion: the dir is gone but git still holds a prunable registration + the branch.
	if err := os.RemoveAll(wt.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Recreate(ctx, "epic-integration", "main"); err != nil {
		t.Fatalf("recreate must survive a ghost registration + leftover branch: %v", err)
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

// TestStatusReportsBehind: after the base branch advances past a worktree's
// branch point, Status reports how many commits the worktree is behind.
func TestStatusReportsBehind(t *testing.T) {
	repo := newRepo(t)
	m := New(repo, mustStore(t))
	ctx := context.Background()

	if _, err := m.Create(ctx, "or-bhd", "main"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Advance main beyond the worktree's branch point (shared refs).
	if err := os.WriteFile(filepath.Join(repo, "more.md"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-m", "advance main")

	st, err := m.Status("or-bhd")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Behind < 1 {
		t.Fatalf("Behind = %d, want >= 1 (main advanced past the branch)", st.Behind)
	}
	// Behind is informational: a worktree that is only behind (clean tree, not ahead)
	// is still Clean.
	if !st.Clean {
		t.Errorf("Clean should be true when only Behind (uncommitted=%v ahead=%d), got false", st.Uncommitted, st.Ahead)
	}
}

// TestRemovePreservesUncommittedWorkAsWipCommit: --force over a dirty worktree
// does not silently lose work — the dirty changes are snapshotted as a WIP commit
// on the branch before the tree is deleted.
func TestRemovePreservesUncommittedWorkAsWipCommit(t *testing.T) {
	repo := newRepo(t)
	m := New(repo, mustStore(t))
	ctx := context.Background()

	wt, err := m.Create(ctx, "or-wip", "main")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	before, _ := runGit(wt.Path, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(wt.Path, "scratch.txt"), []byte("precious\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.Remove(ctx, "or-wip", RemoveOpts{Force: true}); err != nil {
		t.Fatalf("forced remove: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree dir should be gone after forced remove")
	}
	// The branch advanced with a WIP commit preserving the work.
	after, err := runGit(repo, "rev-parse", "or-wip")
	if err != nil {
		t.Fatalf("branch or-wip should still exist with preserved work: %v", err)
	}
	if strings.TrimSpace(after) == strings.TrimSpace(before) {
		t.Fatal("expected a WIP commit on the branch preserving the dirty work")
	}
	out, err := runGit(repo, "show", "--stat", "or-wip")
	if err != nil || !strings.Contains(out, "scratch.txt") {
		t.Fatalf("WIP commit should contain scratch.txt; show=%q err=%v", out, err)
	}
}

// TestReconcileReclaimsStaleViaLivenessHook: a worktree whose owning agent is no
// longer alive (per the injected predicate) is reclaimed by Reconcile; a live one
// survives.
func TestReconcileReclaimsStaleViaLivenessHook(t *testing.T) {
	repo := newRepo(t)
	m := New(repo, mustStore(t)).WithLivenessCheck(func(id string) bool {
		return id != "or-dead" // or-dead's owner is gone
	})
	ctx := context.Background()

	live, err := m.Create(ctx, "or-live", "main")
	if err != nil {
		t.Fatalf("create live: %v", err)
	}
	dead, err := m.Create(ctx, "or-dead", "main")
	if err != nil {
		t.Fatalf("create dead: %v", err)
	}

	if err := m.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, err := os.Stat(dead.Path); !os.IsNotExist(err) {
		t.Fatalf("stale worktree should be reclaimed/removed")
	}
	if _, err := os.Stat(live.Path); err != nil {
		t.Fatalf("live worktree should survive reconcile: %v", err)
	}
}

// TestRemoveWithBranchOneStep (or-kt5): teardown removes the worktree FIRST,
// then the branch — one step, no 'used by worktree' error; safety refusals
// still apply; a missing branch is not an error.
func TestRemoveWithBranchOneStep(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	m := New(repo, nil)
	wt, err := m.Create(ctx, "orion-change-x", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	// Raw branch -D while the worktree exists fails — the friction this fixes.
	if _, err := runGit(repo, "branch", "-D", "orion-change-x"); err == nil {
		t.Fatal("precondition: raw branch -D should fail while the worktree holds the branch")
	}

	if err := m.RemoveWithBranch(ctx, "orion-change-x", RemoveOpts{Force: true}); err != nil {
		t.Fatalf("one-step teardown: %v", err)
	}
	if _, err := os.Stat(wt.Path); err == nil {
		t.Fatal("the worktree must be gone")
	}
	if out, _ := runGit(repo, "branch", "--list", "orion-change-x"); strings.TrimSpace(out) != "" {
		t.Fatalf("the branch must be gone, got %q", out)
	}
	// Idempotence-ish: removing again errors on the unknown worktree (Remove's
	// contract), never on the branch.
	if err := m.RemoveWithBranch(ctx, "orion-change-x", RemoveOpts{Force: true}); err == nil || !strings.Contains(err.Error(), "unknown worktree") {
		t.Fatalf("re-removal surfaces the worktree error: %v", err)
	}
}
