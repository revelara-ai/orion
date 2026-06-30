package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

// TestAcquireLeaseRefusesOverlap: a path-prefix collision is refused; disjoint scopes coexist.
func TestAcquireLeaseRefusesOverlap(t *testing.T) {
	in := New("", "head", nil)
	if err := in.AcquireLease("a", []string{"internal/foo"}); err != nil {
		t.Fatal(err)
	}
	if err := in.AcquireLease("b", []string{"internal/bar"}); err != nil {
		t.Errorf("disjoint scope should be allowed: %v", err)
	}
	if err := in.AcquireLease("c", []string{"internal/foo/baz"}); err == nil {
		t.Error("a sub-path of an active lease must be refused")
	}
	in.ReleaseLease("a")
	if err := in.AcquireLease("c", []string{"internal/foo/baz"}); err != nil {
		t.Errorf("after release, the scope should be acquirable: %v", err)
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
	out, err := in.Integrate(context.Background(), "a", wt, "clusterA")
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
	out, err := in.Integrate(context.Background(), "b", wt, "clusterB")
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
	if _, err := in.Integrate(context.Background(), "x", wt, "cX"); err != nil {
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

	if out, err := in.Integrate(context.Background(), "a", wtA, "cA"); err != nil || out != Integrated {
		t.Fatalf("first integration should succeed: %s %v", out, err)
	}
	headAfterA := git(t, headDir, "rev-parse", "HEAD")

	out, err := in.Integrate(context.Background(), "b", wtB, "cB")
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
