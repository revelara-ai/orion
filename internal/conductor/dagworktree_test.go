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

// withGitRepo creates a fresh, empty git repo with an initial commit and makes it
// the working directory, so a build (which now requires a git repo for worktree
// isolation) runs in an isolated greenfield repo rather than Orion's own source.
func withGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=orion", "GIT_AUTHOR_EMAIL=orion@local",
		"GIT_COMMITTER_NAME=orion", "GIT_COMMITTER_EMAIL=orion@local")
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("commit", "--allow-empty", "-m", "init") // a base commit so worktrees can branch off HEAD
	t.Chdir(dir)
	return dir
}

// Each cluster gets its own distinct git worktree off the base; generation writes
// only inside it; the worktrees are removed by the returned cleanup (or-tcs.1.3).
func TestEachClusterBuildsInIsolatedWorktree(t *testing.T) {
	repo := withGitRepo(t)
	mgr := worktree.New(repo, openStore(t))
	clusters := []decomposer.TaskCluster{
		{Key: "clusterA", Members: []string{"a1"}},
		{Key: "clusterB", Members: []string{"b1"}},
	}
	paths, cleanup, err := clusterWorktreeSet(context.Background(), mgr, clusters, "HEAD")
	if err != nil {
		t.Fatalf("clusterWorktreeSet: %v", err)
	}

	// Distinct worktree per cluster.
	if len(paths) != 2 || paths["clusterA"] == "" || paths["clusterB"] == "" || paths["clusterA"] == paths["clusterB"] {
		t.Fatalf("expected 2 distinct cluster worktrees, got %v", paths)
	}
	for key, p := range paths {
		if _, statErr := os.Stat(p); statErr != nil {
			t.Fatalf("worktree for %s missing at %s: %v", key, p, statErr)
		}
	}

	// Generation writes inside the cluster's own worktree.
	if err := os.WriteFile(filepath.Join(paths["clusterA"], "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("cluster worktree not writable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths["clusterB"], "main.go")); !os.IsNotExist(err) {
		t.Fatalf("writing into clusterA leaked into clusterB's worktree (not isolated)")
	}

	// Cleanup removes every worktree it created.
	cleanup()
	for key, p := range paths {
		if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
			t.Fatalf("worktree for %s not removed after cleanup (stat err=%v)", key, statErr)
		}
	}
}
