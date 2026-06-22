package conductor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// gitInitRepo makes a temp git repo with a `main` branch + one commit, and leaves an
// uncommitted change in the working tree (to prove delivery doesn't disturb it).
func gitInitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# dev project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "initial", "--no-verify")
	// an in-progress, uncommitted edit in the developer's working tree
	if err := os.WriteFile(filepath.Join(dir, "WIP.txt"), []byte("work in progress\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeProvenBuild(t *testing.T, dir string) {
	t.Helper()
	must := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("go.mod", "module orion-generated/svc\n\ngo 1.25\n")
	must("main.go", "package main\n\nfunc main() {}\n")
}

// TestGitDeliverCommitsOnWorktreeBranchWithoutTouchingWorkingTree: proven code is
// committed onto the orion-<slug> branch in a WORKTREE, with the provenance message;
// the developer's working tree (uncommitted WIP on main) is left exactly as it was.
func TestGitDeliverCommitsOnWorktreeBranchWithoutTouchingWorkingTree(t *testing.T) {
	if testing.Short() {
		t.Skip("git worktree + commit")
	}
	ctx := context.Background()
	repo := gitInitRepo(t)
	build := t.TempDir()
	writeProvenBuild(t, build)

	es := spec.ExecutableSpec{
		Intent:           "Build an HTTP service that returns the current time.",
		Hash:             "d788da4e0000000000000000000000000000000000000000000000000000abcd",
		ResponseContract: spec.ResponseContract{Route: "/time", Port: 8080, ContentType: "application/json"},
	}

	d, err := GitDeliver(ctx, repo, nil, build, es)
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if d.Branch != "orion-time-service" || d.Commit == "" {
		t.Fatalf("unexpected delivery: %+v", d)
	}

	// The proven code + provenance are on the branch.
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	files := git("ls-tree", "-r", "--name-only", d.Branch)
	if !strings.Contains(files, "time-service/main.go") || !strings.Contains(files, "time-service/ORION.md") {
		t.Fatalf("proven code not on the branch:\n%s", files)
	}
	msg := git("log", "-1", "--format=%B", d.Branch)
	if !strings.Contains(msg, "proven") || !strings.Contains(msg, "Accept") {
		t.Fatalf("commit message lacks provenance:\n%s", msg)
	}

	// The developer's working tree is UNTOUCHED: still on main, WIP.txt still uncommitted.
	if br := git("rev-parse", "--abbrev-ref", "HEAD"); br != "main" {
		t.Fatalf("delivery moved the developer off main → %q", br)
	}
	if st := git("status", "--porcelain"); !strings.Contains(st, "WIP.txt") {
		t.Fatalf("delivery disturbed the developer's working tree (WIP.txt gone):\n%q", st)
	}
}
