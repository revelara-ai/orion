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

	headDir, ok, err := integrateEpic(ctx, mgr, clusters, clusterWT, results, "main", green, nil)
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

	if _, ok, err := integrateEpic(ctx, mgr, clusters, clusterWT, results, "main", red, nil); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("a red post-merge re-proof must fail the epic assembly (ok=false)")
	}
}
