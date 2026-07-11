package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// setup builds a repo with an integration "head" branch, a head worktree checked out on it, and a
// helper to add a cluster worktree (a branch off head with a commit touching one file).
func setup(t *testing.T) (root, headDir string, addCluster func(branch, file, content string) string) {
	t.Helper()
	parent := t.TempDir()
	root = filepath.Join(parent, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "config", "user.email", "t@example.com")
	git(t, root, "config", "user.name", "T")
	mustWrite(t, filepath.Join(root, "base.txt"), "base\n")
	git(t, root, "add", "-A")
	git(t, root, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "base")
	git(t, root, "branch", "head")

	headDir = filepath.Join(parent, "head")
	git(t, root, "worktree", "add", "-q", headDir, "head")

	addCluster = func(branch, file, content string) string {
		wt := filepath.Join(parent, branch)
		git(t, root, "worktree", "add", "-q", "-b", branch, wt, "head")
		mustWrite(t, filepath.Join(wt, file), content)
		git(t, wt, "add", "-A")
		git(t, wt, "-c", "commit.gpgsign=false", "commit", "-q", "-m", branch+" change")
		return wt
	}
	return root, headDir, addCluster
}

// TestTryAcquireLeaseRefusesOverlap: a path-prefix collision is refused; disjoint scopes coexist.
func TestTryAcquireLeaseRefusesOverlap(t *testing.T) {
	in := New("", "head", nil)
	if err := in.TryAcquireLease("a", []string{"internal/foo"}); err != nil {
		t.Fatal(err)
	}
	if err := in.TryAcquireLease("b", []string{"internal/bar"}); err != nil {
		t.Errorf("disjoint scope should be allowed: %v", err)
	}
	if err := in.TryAcquireLease("c", []string{"internal/foo/baz"}); err == nil {
		t.Error("a sub-path of an active lease must be refused")
	}
	in.ReleaseLease("a")
	if err := in.TryAcquireLease("c", []string{"internal/foo/baz"}); err != nil {
		t.Errorf("after release, the scope should be acquirable: %v", err)
	}
}

// TestEmptyScopeIsExclusive: an UNDECLARED (empty) scope leases the whole tree — same fail-safe
// as the dispatch-time leases (conductor/dag.go): a task that declares nothing could touch
// anything, so it must exclude every other lease (and be excluded by any held lease).
func TestEmptyScopeIsExclusive(t *testing.T) {
	in := New("", "head", nil)
	if err := in.TryAcquireLease("a", nil); err != nil {
		t.Fatal(err)
	}
	if err := in.TryAcquireLease("b", []string{"docs/x"}); err == nil {
		t.Error("an empty scope must lease the whole tree: any other acquire must be refused")
	}
	in.ReleaseLease("a")
	if err := in.TryAcquireLease("b", []string{"docs/x"}); err != nil {
		t.Fatal(err)
	}
	if err := in.TryAcquireLease("c", nil); err == nil {
		t.Error("a whole-tree (empty scope) acquire must be refused while any lease is held")
	}
}

// TestAcquireLeaseBlocksUntilRelease: the blocking acquire waits while an overlapping lease is
// held, wakes when it is released, and honours context cancellation while waiting.
func TestAcquireLeaseBlocksUntilRelease(t *testing.T) {
	in := New("", "head", nil)
	if err := in.TryAcquireLease("a", []string{"internal/foo"}); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- in.AcquireLease(context.Background(), "b", []string{"internal/foo/sub"}) }()
	select {
	case err := <-done:
		t.Fatalf("overlapping acquire must block while the lease is held; returned early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	in.ReleaseLease("a")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("acquire after release should succeed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked acquire did not wake after the overlapping lease was released")
	}

	// "b" now holds internal/foo/sub; a cancelled context must abort an overlapping wait.
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() { cancelled <- in.AcquireLease(ctx, "c", []string{"internal/foo"}) }()
	cancel()
	select {
	case err := <-cancelled:
		if err == nil {
			t.Fatal("a cancelled context must fail the blocking acquire")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocking acquire ignored context cancellation")
	}
}

// TestOverlappingIntegrationsSerialize is the S1 wiring test (or-1lz): Integrate itself must HOLD
// the file-scope lease for the whole integration, so two integrations with OVERLAPPING declared
// scope serialize (the second blocks until the first releases) while a DISJOINT scope stays
// acquirable throughout. The two clusters touch DIFFERENT files, so git alone would happily merge
// them — only the lease provides the mutual exclusion being asserted.
func TestOverlappingIntegrationsSerialize(t *testing.T) {
	_, headDir, addCluster := setup(t)
	wtA := addCluster("cA", "src_a.txt", "A\n")
	wtB := addCluster("cB", "src_b.txt", "B\n")

	entered := make(chan struct{}) // closed when A is mid-integration (inside its re-proof)
	release := make(chan struct{}) // closed to let A finish
	var once sync.Once
	in := New(headDir, "head", func(context.Context, string) (bool, error) {
		once.Do(func() { close(entered); <-release })
		return true, nil
	})

	scope := []string{"src"} // both clusters declare the same scope → S1 says they serialize
	aDone := make(chan error, 1)
	var aOut Outcome
	go func() {
		out, err := in.Integrate(context.Background(), "a", wtA, "cA", scope)
		aOut = out
		aDone <- err
	}()
	<-entered

	// A is mid-integration: its lease must be LIVE — an overlapping acquire is refused...
	if err := in.TryAcquireLease("probe", []string{"src/x"}); err == nil {
		t.Error("overlapping scope must be refused while an integration holds the lease")
		in.ReleaseLease("probe")
	}
	// ...while a disjoint scope proceeds concurrently (leases only exclude overlap).
	if err := in.TryAcquireLease("probe2", []string{"docs"}); err != nil {
		t.Errorf("disjoint scope should be acquirable during an integration: %v", err)
	}
	in.ReleaseLease("probe2")

	// B (overlapping scope) must BLOCK until A releases.
	bDone := make(chan error, 1)
	var bOut Outcome
	go func() {
		out, err := in.Integrate(context.Background(), "b", wtB, "cB", scope)
		bOut = out
		bDone <- err
	}()
	select {
	case err := <-bDone:
		t.Fatalf("overlapping-scope integration must block until the first releases; returned: %v %v", bOut, err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	if err := <-aDone; err != nil {
		t.Fatal(err)
	}
	if aOut != Integrated {
		t.Fatalf("first integration: want Integrated, got %s", aOut)
	}
	select {
	case err := <-bDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("second integration never completed after the first released its lease")
	}
	if bOut != Integrated {
		t.Fatalf("second integration: want Integrated, got %s", bOut)
	}
	for _, f := range []string{"src_a.txt", "src_b.txt"} {
		if _, err := os.Stat(filepath.Join(headDir, f)); err != nil {
			t.Errorf("integrated head must contain %s: %v", f, err)
		}
	}
}

// TestLeaseReleasedOnConflict: an integration that ends in Conflict must still release its lease
// (release on ALL exit paths), or the scope would be locked forever.
func TestLeaseReleasedOnConflict(t *testing.T) {
	_, headDir, addCluster := setup(t)
	wtA := addCluster("cA", "shared.txt", "from A\n")
	wtB := addCluster("cB", "shared.txt", "from B\n")
	in := New(headDir, "head", func(context.Context, string) (bool, error) { return true, nil })

	if out, err := in.Integrate(context.Background(), "a", wtA, "cA", []string{"shared.txt"}); err != nil || out != Integrated {
		t.Fatalf("first integration should succeed: %s %v", out, err)
	}
	// The SECOND acquiring the same scope also proves the first released it on success.
	out, err := in.Integrate(context.Background(), "b", wtB, "cB", []string{"shared.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if out != Conflict {
		t.Fatalf("want Conflict, got %s", out)
	}
	if err := in.TryAcquireLease("probe", []string{"shared.txt"}); err != nil {
		t.Errorf("lease must be released after a Conflict outcome: %v", err)
	}
}

// TestLeaseReleasedOnRolledBack: a RED re-proof rolls the head back — and must still release the
// task's lease.
func TestLeaseReleasedOnRolledBack(t *testing.T) {
	_, headDir, addCluster := setup(t)
	wt := addCluster("cB", "b.txt", "B\n")
	in := New(headDir, "head", func(context.Context, string) (bool, error) { return false, nil })

	out, err := in.Integrate(context.Background(), "b", wt, "cB", []string{"b.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if out != RolledBack {
		t.Fatalf("want RolledBack, got %s", out)
	}
	if err := in.TryAcquireLease("probe", []string{"b.txt"}); err != nil {
		t.Errorf("lease must be released after a RolledBack outcome: %v", err)
	}
}

// TestIntegrateAdvancesHeadOnGreen: a clean cluster rebases + ff-merges onto head, the re-proof is
// green, and the head advances to include the cluster's change.
func TestIntegrateAdvancesHeadOnGreen(t *testing.T) {
	root, headDir, addCluster := setup(t)
	_ = root
	wt := addCluster("clusterA", "a.txt", "A\n")
	greenReprove := func(context.Context, string) (bool, error) { return true, nil }
	in := New(headDir, "head", greenReprove)

	before := git(t, headDir, "rev-parse", "HEAD")
	out, err := in.Integrate(context.Background(), "a", wt, "clusterA", []string{"a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if out != Integrated {
		t.Fatalf("want Integrated, got %s", out)
	}
	after := git(t, headDir, "rev-parse", "HEAD")
	if after == before {
		t.Error("head should have advanced")
	}
	if _, err := os.Stat(filepath.Join(headDir, "a.txt")); err != nil {
		t.Errorf("the cluster's file should be on the integrated head: %v", err)
	}
}

// TestIntegrateRollsBackOnRedReproof: the merge happens, but a RED re-proof on the merged tree
// rolls the head back to exactly where it was — assembly that breaks the whole is rejected.
func TestIntegrateRollsBackOnRedReproof(t *testing.T) {
	_, headDir, addCluster := setup(t)
	wt := addCluster("clusterB", "b.txt", "B\n")
	redReprove := func(context.Context, string) (bool, error) { return false, nil }
	in := New(headDir, "head", redReprove)

	before := git(t, headDir, "rev-parse", "HEAD")
	out, err := in.Integrate(context.Background(), "b", wt, "clusterB", []string{"b.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if out != RolledBack {
		t.Fatalf("want RolledBack, got %s", out)
	}
	after := git(t, headDir, "rev-parse", "HEAD")
	if after != before {
		t.Errorf("head must be reset to before (%s), got %s", before, after)
	}
	if _, err := os.Stat(filepath.Join(headDir, "b.txt")); err == nil {
		t.Error("the rolled-back cluster's file must NOT be on the head")
	}
}

// TestInIntegrationPredicate: the task is mid-integration DURING the merge (observed inside the
// re-proof hook) and not before/after — this is what worktree.WithIntegrationCheck gates on.
func TestInIntegrationPredicate(t *testing.T) {
	_, headDir, addCluster := setup(t)
	wt := addCluster("cX", "x.txt", "X\n")
	var in *Integrator
	in = New(headDir, "head", func(context.Context, string) (bool, error) {
		if !in.InIntegration("x") {
			t.Error("task must be mid-integration while its merged tree is being re-proven")
		}
		return true, nil
	})
	if in.InIntegration("x") {
		t.Error("task should not be mid-integration before Integrate")
	}
	if _, err := in.Integrate(context.Background(), "x", wt, "cX", nil); err != nil {
		t.Fatal(err)
	}
	if in.InIntegration("x") {
		t.Error("task should not be mid-integration after Integrate")
	}
}

// TestIntegrateConflictLeavesHeadUntouched: two clusters editing the SAME file — the first
// integrates, the second's rebase conflicts and the head is left exactly as the first left it.
func TestIntegrateConflictLeavesHeadUntouched(t *testing.T) {
	_, headDir, addCluster := setup(t)
	wtA := addCluster("cA", "shared.txt", "from A\n")
	wtB := addCluster("cB", "shared.txt", "from B\n")
	in := New(headDir, "head", func(context.Context, string) (bool, error) { return true, nil })

	if out, err := in.Integrate(context.Background(), "a", wtA, "cA", []string{"shared.txt"}); err != nil || out != Integrated {
		t.Fatalf("first integration should succeed: %s %v", out, err)
	}
	headAfterA := git(t, headDir, "rev-parse", "HEAD")

	out, err := in.Integrate(context.Background(), "b", wtB, "cB", []string{"shared.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if out != Conflict {
		t.Fatalf("the conflicting second integration must report Conflict, got %s", out)
	}
	if got := git(t, headDir, "rev-parse", "HEAD"); got != headAfterA {
		t.Errorf("head must be untouched on conflict: want %s, got %s", headAfterA, got)
	}
}
