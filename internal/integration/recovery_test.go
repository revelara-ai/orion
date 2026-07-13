package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/worktree"
)

// TestWorktreeBaselineRestoredAfterRollback (or-dka, bead-named): a red
// re-proof rolls the head back to the FULL baseline — tracked files reset
// AND the proof scratch the failed re-proof left behind removed — so later
// merges on this head are never contaminated by a rolled-back attempt.
func TestWorktreeBaselineRestoredAfterRollback(t *testing.T) {
	_, headDir, addCluster := setup(t)
	addCluster("cl-red", "red.txt", "red\n")

	before := git(t, headDir, "rev-parse", "HEAD")
	redReprove := func(_ context.Context, dir string) (bool, error) {
		// The failing re-proof litters the head with untracked + ignored scratch,
		// exactly like a real proof pass.
		mustWrite(t, filepath.Join(dir, "synth_corpus_test.go"), "package main\n")
		if err := os.MkdirAll(filepath.Join(dir, ".orion-gocache"), 0o755); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, filepath.Join(dir, ".orion-gocache", "junk"), "x")
		return false, nil
	}
	integ := New(headDir, "head", redReprove)

	out, err := integ.Integrate(context.Background(), "t-red", filepath.Join(filepath.Dir(headDir), "cl-red"), "cl-red", []string{"red.txt"})
	if err != nil {
		t.Fatalf("integrate: %v", err)
	}
	if out != RolledBack {
		t.Fatalf("red re-proof must roll back, got %s", out)
	}
	if got := git(t, headDir, "rev-parse", "HEAD"); got != before {
		t.Fatalf("head not restored: %s != %s", got, before)
	}
	// The FULL baseline: no scratch survives the rollback.
	for _, leftover := range []string{"synth_corpus_test.go", ".orion-gocache", "red.txt"} {
		if _, err := os.Stat(filepath.Join(headDir, leftover)); !os.IsNotExist(err) {
			t.Fatalf("rolled-back head still carries %s — later re-proofs would be contaminated", leftover)
		}
	}
	if st := git(t, headDir, "status", "--porcelain"); st != "" {
		t.Fatalf("head worktree not at baseline after rollback:\n%s", st)
	}
}

// TestIntegrationLockStaleLockRecoveryOnRestart (or-dka, bead-named — adapted
// to the actual design): the integrator's locks are in-memory and DIE WITH
// THE PROCESS; crash recovery is by RECONSTRUCTION — the next run's
// worktree.Recreate discards the dead run's head branch (including a
// half-merged, never-proven commit and a dirty tree) and rebuilds it from
// base. This test pins that design: a refactor that resumes stale heads
// (e.g. switching to CreateResume for incremental integration) must
// consciously re-own crash recovery.
func TestIntegrationLockStaleLockRecoveryOnRestart(t *testing.T) {
	root, headDir, addCluster := setup(t)
	clusterDir := addCluster("cl-crash", "crash.txt", "crash\n")
	base := git(t, headDir, "rev-parse", "HEAD")

	// Simulate the DEAD RUN: the merge landed on the head branch, the re-proof
	// never completed (process died), scratch litters the tree.
	git(t, clusterDir, "rebase", "head")
	git(t, headDir, "merge", "--ff-only", "cl-crash")
	mustWrite(t, filepath.Join(headDir, ".orion-gocache-junk"), "half-done")
	if got := git(t, headDir, "rev-parse", "HEAD"); got == base {
		t.Fatal("fixture: the dead run must have advanced the head")
	}

	// The NEXT RUN: integrateEpic's construction path — Recreate the head
	// worktree from base. The stale branch state must not wedge or leak.
	mgr := worktree.New(root, nil)
	// The dead run's worktree registration is still present under a DIFFERENT
	// path than the manager would choose; recreate a fresh head under the
	// manager's own layout, from the surviving base rev.
	wt, err := mgr.Recreate(context.Background(), "epic-integration", base)
	if err != nil {
		t.Fatalf("restart recovery must not wedge on stale state: %v", err)
	}
	if got := git(t, wt.Path, "rev-parse", "HEAD"); got != base {
		t.Fatalf("recovered head must start at base, got %s want %s", got, base)
	}
	if st := git(t, wt.Path, "status", "--porcelain"); st != "" {
		t.Fatalf("recovered head must be clean:\n%s", st)
	}
}
