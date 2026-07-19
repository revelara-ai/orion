package conductor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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

	headDir, ok, err := integrateEpic(ctx, mgr, clusters, clusterWT, results, "main", green, nil, nil, nil, nil)
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
	if _, ok, err := integrateEpic(ctx, mgr, clusters, clusterWT, results, "main", green, nil, nil, nil, nil); err != nil || !ok {
		t.Fatalf("first run: ok=%v err=%v", ok, err)
	}
	// Second run (the re-run) must integrate cleanly despite the leftover epic-integration branch.
	headDir, ok, err := integrateEpic(ctx, mgr, clusters, clusterWT, results, "main", green, nil, nil, nil, nil)
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

	if _, ok, err := integrateEpic(ctx, mgr, clusters, clusterWT, results, "main", red, nil, nil, nil, nil); err != nil {
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

	headDir, ok, err := integrateEpic(ctx, mgr, clusters, clusterWT, results, "main", green, nil, nil, nil, nil)
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

// waveClusters builds K accepted clusters with the given file scopes.
func waveClusters(t *testing.T, mgr *worktree.Manager, scopes map[string]string) ([]decomposer.TaskCluster, map[string]string, []taskResult) {
	t.Helper()
	ctx := context.Background()
	var clusters []decomposer.TaskCluster
	clusterWT := map[string]string{}
	var results []taskResult
	keys := make([]string, 0, len(scopes))
	for k := range scopes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cl := decomposer.TaskCluster{Key: key, Members: []string{key + "-t"}}
		if scopes[key] != "" {
			cl.FileScopes = []string{scopes[key]}
		}
		wt, err := mgr.Create(ctx, key, "main")
		if err != nil {
			t.Fatal(err)
		}
		sub := scopes[key]
		if sub == "" {
			sub = key
		}
		if err := os.MkdirAll(filepath.Join(wt.Path, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		dogWrite(t, filepath.Join(wt.Path, sub, key+".go"), "package "+strings.ReplaceAll(sub, "/", "")+"\n")
		clusters = append(clusters, cl)
		clusterWT[key] = wt.Path
		results = append(results, taskResult{TaskID: key + "-t", Verdict: "Accept"})
	}
	return clusters, clusterWT, results
}

// TestAssemblyWaveBatching (or-7et.4a acceptance): K lease-disjoint accepted
// clusters re-prove ONCE per wave plus the bookend — not once per cluster.
func TestAssemblyWaveBatching(t *testing.T) {
	ctx := context.Background()
	repo := initManagedRepo(t)
	mgr := worktree.New(repo, openStore(t)).WithBase("main")
	// Three disjoint scopes → ONE wave; spy = 1 wave + 1 bookend = 2, not 3.
	clusters, clusterWT, results := waveClusters(t, mgr, map[string]string{
		"wa": "pkga", "wb": "pkgb", "wc": "pkgc",
	})
	calls := 0
	spy := func(context.Context, string) (bool, error) { calls++; return true, nil }

	_, ok, err := integrateEpic(ctx, mgr, clusters, clusterWT, results, "main", spy, nil, nil, nil, nil)
	if err != nil || !ok {
		t.Fatalf("assembly must succeed: ok=%v err=%v", ok, err)
	}
	if calls != 2 {
		t.Fatalf("3 disjoint clusters = 1 wave + 1 bookend = 2 re-proofs, got %d", calls)
	}
}

// TestWaveRollback (or-7et.4 acceptance): a red wave re-proof resets the head
// to the PRE-WAVE rev — previously integrated waves survive on the head.
func TestWaveRollback(t *testing.T) {
	ctx := context.Background()
	repo := initManagedRepo(t)
	mgr := worktree.New(repo, openStore(t)).WithBase("main")
	// Overlapping scopes → each cluster is its own wave (deterministic order: wa then wb).
	clusters, clusterWT, results := waveClusters(t, mgr, map[string]string{
		"wa": "shared/a", "wb": "shared",
	})
	waveN := 0
	waves := func(_ context.Context, _ string, _ []decomposer.TaskCluster, _ string) (bool, error) {
		waveN++
		return waveN == 1, nil // wave 1 green, wave 2 RED
	}
	full := func(context.Context, string) (bool, error) { return true, nil }

	headDir, ok, err := integrateEpic(ctx, mgr, clusters, clusterWT, results, "main", full, waves, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a red wave must fail the epic")
	}
	if waveN != 2 {
		t.Fatalf("overlapping scopes must form 2 waves, got %d", waveN)
	}
	if _, err := os.Stat(filepath.Join(headDir, "shared/a", "wa.go")); err != nil {
		t.Fatal("wave 1's files must SURVIVE the wave-2 rollback")
	}
	if _, err := os.Stat(filepath.Join(headDir, "shared", "wb.go")); err == nil {
		t.Fatal("the red wave's files must be rolled back off the head")
	}
}

// TestFinalBookendFullProof (or-7et.4b acceptance): the final head always gets
// the FULL re-proof; a red bookend rejects the epic even when every wave was
// green.
func TestFinalBookendFullProof(t *testing.T) {
	ctx := context.Background()
	repo := initManagedRepo(t)
	mgr := worktree.New(repo, openStore(t)).WithBase("main")
	clusters, clusterWT, results := waveClusters(t, mgr, map[string]string{
		"wa": "pkga", "wb": "pkgb",
	})
	greenWaves := func(context.Context, string, []decomposer.TaskCluster, string) (bool, error) { return true, nil }
	bookends := 0
	redBookend := func(context.Context, string) (bool, error) { bookends++; return false, nil }

	_, ok, err := integrateEpic(ctx, mgr, clusters, clusterWT, results, "main", redBookend, greenWaves, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a red bookend must reject the epic even after green waves")
	}
	if bookends != 1 {
		t.Fatalf("the full bookend must run exactly once, got %d", bookends)
	}
}

// TestScopedReproveEscalation (or-7et.4b acceptance): an undeclared FileScope
// in the wave, or a wave diff touching go.mod, forces the FULL re-proof.
func TestScopedReproveEscalation(t *testing.T) {
	ctx := context.Background()
	fullCalls := 0
	full := func(context.Context, string) (bool, error) { fullCalls++; return true, nil }
	wr := scopedWaveReprove(full, "")

	// (i) empty FileScope → full, before any git work.
	ok, err := wr(ctx, t.TempDir(), []decomposer.TaskCluster{{Key: "x", Members: []string{"t"}}}, "HEAD")
	if err != nil || !ok || fullCalls != 1 {
		t.Fatalf("undeclared scope must escalate to full: ok=%v err=%v calls=%d", ok, err, fullCalls)
	}

	// (ii) a wave diff touching go.mod → full.
	repo := initManagedRepo(t)
	pre, _ := gitIn(ctx, repo, "rev-parse", "HEAD")
	dogWrite(t, filepath.Join(repo, "go.mod"), "module svc\n\ngo 1.24\n")
	if _, err := gitIn(ctx, repo, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitIn(ctx, repo, "-c", "user.name=T", "-c", "user.email=t@e.c", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "bump"); err != nil {
		t.Fatal(err)
	}
	scoped := []decomposer.TaskCluster{{Key: "y", Members: []string{"t"}, FileScopes: []string{"pkga"}}}
	ok, err = wr(ctx, repo, scoped, strings.TrimSpace(pre))
	if err != nil || !ok || fullCalls != 2 {
		t.Fatalf("a go.mod-touching wave must escalate to full: ok=%v err=%v calls=%d", ok, err, fullCalls)
	}
}

// TestLazyWorktreeLifecycle (or-7et.4c acceptance): cluster checkouts are gone
// after their wave integrates (eager removal); the assembled head remains.
func TestLazyWorktreeLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := initManagedRepo(t)
	mgr := worktree.New(repo, openStore(t)).WithBase("main")
	clusters, clusterWT, results := waveClusters(t, mgr, map[string]string{
		"wa": "pkga", "wb": "pkgb",
	})
	wtPaths := []string{clusterWT["wa"], clusterWT["wb"]}
	green := func(context.Context, string) (bool, error) { return true, nil }

	headDir, ok, err := integrateEpic(ctx, mgr, clusters, clusterWT, results, "main", green, nil, nil, nil, nil)
	if err != nil || !ok {
		t.Fatalf("assembly: ok=%v err=%v", ok, err)
	}
	for _, p := range wtPaths {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("cluster checkout %s must be removed once its wave integrates", p)
		}
	}
	if _, err := os.Stat(filepath.Join(headDir, "pkga", "wa.go")); err != nil {
		t.Fatal("the assembled head must survive eager cluster-worktree removal")
	}
}

// TestConformanceGateFiresPreMerge (or-7et.5d): integrateEpic consults the
// gate BEFORE merging a cluster — a mismatch stops the assembly with nothing
// merged from that cluster, not an epic-wide post-merge failure.
func TestConformanceGateFiresPreMerge(t *testing.T) {
	ctx := context.Background()
	repo := initManagedRepo(t)
	mgr := worktree.New(repo, openStore(t)).WithBase("main")
	clusters, clusterWT, results := waveClusters(t, mgr, map[string]string{"wa": "pkga", "wb": "pkgb"})
	green := func(context.Context, string) (bool, error) { return true, nil }
	gateCalls := 0
	conform := func(cl decomposer.TaskCluster) error {
		gateCalls++
		if cl.Key == "wa" {
			return fmt.Errorf("interface conformance: task wa-t requires Note")
		}
		return nil
	}

	headDir, ok, err := integrateEpic(ctx, mgr, clusters, clusterWT, results, "main", green, nil, conform, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a conformance failure must stop the assembly")
	}
	if gateCalls == 0 {
		t.Fatal("the gate must be consulted pre-merge")
	}
	if _, err := os.Stat(filepath.Join(headDir, "pkga", "wa.go")); err == nil {
		t.Fatal("the non-conforming cluster must NOT merge onto the head")
	}
}
