package conductor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/worktree"
)

func initManagedRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "T")
	dogWrite(t, filepath.Join(dir, "go.mod"), "module svc\n\ngo 1.23\n")
	git("add", "-A")
	git("-c", "commit.gpgsign=false", "commit", "-q", "-m", "base")
	return dir
}

// TestIntegrateEpicAssemblesClustersOntoHead (or-tcs.1.6): two accepted clusters integrate ONE AT
// A TIME onto a fresh epic head; the integrated head carries BOTH clusters' files (the assembled
// whole, not one cluster's tree), and the re-proof on each merge is green.
func TestIntegrateEpicAssemblesClustersOntoHead(t *testing.T) {
	ctx := context.Background()
	repo := initManagedRepo(t)
	mgr := worktree.New(repo, openStore(t)).WithBase("main")
	clusters := []decomposer.TaskCluster{
		{Key: "clA", Members: []string{"a1"}},
		{Key: "clB", Members: []string{"b1"}},
	}
	clusterWT := map[string]string{}
	for _, cl := range clusters {
		wt, err := mgr.Create(ctx, cl.Key, "main")
		if err != nil {
			t.Fatal(err)
		}
		clusterWT[cl.Key] = wt.Path
		dogWrite(t, filepath.Join(wt.Path, cl.Key+".go"), "package svc\n\n// "+cl.Key+"\n")
	}
	results := []taskResult{{TaskID: "a1", Verdict: "Accept"}, {TaskID: "b1", Verdict: "Accept"}}
	green := func(context.Context, string) (bool, error) { return true, nil }

	headDir, ok, err := integrateEpic(ctx, mgr, clusters, clusterWT, results, "main", green, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("both accepted clusters should integrate cleanly")
	}
	for _, f := range []string{"clA.go", "clB.go"} {
		if _, err := os.Stat(filepath.Join(headDir, f)); err != nil {
			t.Errorf("the integrated head must carry %s (the assembled whole): %v", f, err)
		}
	}
}

// TestIntegrateEpicIsIdempotentOnReRun (or-d3w): a second integrateEpic on the same repo must not
// crash on the epic-integration branch the first run left behind — it recreates a fresh head. This
// is the `orion run` re-run case that failed with "a branch named epic-integration already exists".
func TestIntegrateEpicIsIdempotentOnReRun(t *testing.T) {
	ctx := context.Background()
	repo := initManagedRepo(t)
	mgr := worktree.New(repo, openStore(t)).WithBase("main")
	clusters := []decomposer.TaskCluster{{Key: "clA", Members: []string{"a1"}}}
	wt, err := mgr.Create(ctx, "clA", "main")
	if err != nil {
		t.Fatal(err)
	}
	dogWrite(t, filepath.Join(wt.Path, "clA.go"), "package svc\n\n// clA\n")
	clusterWT := map[string]string{"clA": wt.Path}
	results := []taskResult{{TaskID: "a1", Verdict: "Accept"}}
	green := func(context.Context, string) (bool, error) { return true, nil }

	// First run creates the epic-integration head + branch.
	if _, ok, err := integrateEpic(ctx, mgr, clusters, clusterWT, results, "main", green, nil, nil); err != nil || !ok {
		t.Fatalf("first run: ok=%v err=%v", ok, err)
	}
	// Second run (the re-run) must integrate cleanly despite the leftover epic-integration branch.
	headDir, ok, err := integrateEpic(ctx, mgr, clusters, clusterWT, results, "main", green, nil, nil)
	if err != nil || !ok {
		t.Fatalf("re-run must integrate without an epic-integration collision: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(headDir, "clA.go")); err != nil {
		t.Errorf("the re-run head must carry the assembled file: %v", err)
	}
}

// TestLazyWorktreesIdempotentOnReRun (or-d3w + or-7et.4c): allocation is lazy
// (nothing exists until a key is requested), a re-run must not collide on the
// branches a prior run's cleanup left behind (Remove drops the worktree, not
// the branch), and repeated gets return the same checkout.
func TestLazyWorktreesIdempotentOnReRun(t *testing.T) {
	ctx := context.Background()
	repo := initManagedRepo(t)
	mgr := worktree.New(repo, openStore(t)).WithBase("main")

	paths1, get1, cleanup1 := lazyWorktrees(ctx, mgr, "main")
	if len(paths1) != 0 {
		t.Fatal("lazy allocation must create nothing upfront")
	}
	p1, err := get1("scaffold")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := get1("scaffold"); again != p1 {
		t.Fatal("repeated gets must return the same checkout")
	}
	if _, err := get1(""); err == nil {
		t.Fatal("an unclustered task must error, not allocate")
	}
	cleanup1()
	if _, err := os.Stat(p1); err == nil {
		t.Fatal("cleanup must remove the allocated checkout")
	}

	// Second run must NOT collide on the leftover scaffold branch.
	_, get2, cleanup2 := lazyWorktrees(ctx, mgr, "main")
	defer cleanup2()
	if _, err := get2("scaffold"); err != nil {
		t.Fatalf("re-run must not collide on the leftover cluster branch: %v", err)
	}
}

// TestIntegrateEpicRollsBackOnRedAssembly: a RED post-merge re-proof fails the epic — the assembly
// gate is not a rubber stamp. Uses TWO clusters: the re-proof only runs for a non-trivial assembly
// (a single cluster fast-forwards to its already-proven tree, so its re-proof is skipped).
func TestIntegrateEpicRollsBackOnRedAssembly(t *testing.T) {
	ctx := context.Background()
	repo := initManagedRepo(t)
	mgr := worktree.New(repo, openStore(t)).WithBase("main")
	clusters := []decomposer.TaskCluster{
		{Key: "clA", Members: []string{"a1"}},
		{Key: "clB", Members: []string{"b1"}},
	}
	clusterWT := map[string]string{}
	for _, cl := range clusters {
		wt, err := mgr.Create(ctx, cl.Key, "main")
		if err != nil {
			t.Fatal(err)
		}
		clusterWT[cl.Key] = wt.Path
		dogWrite(t, filepath.Join(wt.Path, cl.Key+".go"), "package svc\n")
	}
	results := []taskResult{{TaskID: "a1", Verdict: "Accept"}, {TaskID: "b1", Verdict: "Accept"}}
	red := func(context.Context, string) (bool, error) { return false, nil }

	if _, ok, err := integrateEpic(ctx, mgr, clusters, clusterWT, results, "main", red, nil, nil); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("a red post-merge re-proof must fail the epic assembly (ok=false)")
	}
}

// TestIntegrateEpicAssemblesAcceptedSubset (or-v9f.5): a failed cluster is skipped
// and the ACCEPTED subset still assembles cleanly (ok=true) — the head carries the
// accepted cluster's files and none of the failed one's. This is the artifact
// partial delivery ships.
func TestIntegrateEpicAssemblesAcceptedSubset(t *testing.T) {
	ctx := context.Background()
	repo := initManagedRepo(t)
	mgr := worktree.New(repo, openStore(t)).WithBase("main")
	clusters := []decomposer.TaskCluster{
		{Key: "clOK", Members: []string{"a1"}},
		{Key: "clBAD", Members: []string{"b1"}},
	}
	clusterWT := map[string]string{}
	for _, cl := range clusters {
		wt, err := mgr.Create(ctx, cl.Key, "main")
		if err != nil {
			t.Fatal(err)
		}
		clusterWT[cl.Key] = wt.Path
		dogWrite(t, filepath.Join(wt.Path, cl.Key+".go"), "package svc\n\n// "+cl.Key+"\n")
	}
	results := []taskResult{{TaskID: "a1", Verdict: "Accept"}, {TaskID: "b1", Verdict: "Reject"}}
	green := func(context.Context, string) (bool, error) { return true, nil }

	headDir, ok, err := integrateEpic(ctx, mgr, clusters, clusterWT, results, "main", green, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the accepted subset must assemble cleanly even when a sibling cluster failed")
	}
	if _, err := os.Stat(filepath.Join(headDir, "clOK.go")); err != nil {
		t.Errorf("accepted cluster's file must be on the integration head: %v", err)
	}
	if _, err := os.Stat(filepath.Join(headDir, "clBAD.go")); err == nil {
		t.Error("failed cluster's file must NOT be on the integration head")
	}
}
