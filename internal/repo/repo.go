// Package repo resolves the Orion-managed git repository a project's worktrees
// branch off. Greenfield: git init a fresh repo under the project data dir.
// Brownfield: clone the target. It is the single owner of "where is this project's
// repo and what is its integration base" — a deterministic, shell-verifiable
// primitive feeding internal/worktree.
//
// Manifesto: local-first; side-effect isolation (the managed repo, not the
// developer's working tree, is where agents build).
package repo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// Intake distinguishes how the managed repo is seeded.
type Intake struct {
	Brownfield bool   // false = greenfield (init); true = clone Source
	Source     string // brownfield only: path/URL of the target repo to clone
}

// Repo is the resolved managed git repo a project's worktrees branch off.
type Repo struct {
	Path string // <store.Dir()>/repo
	Base string // integration base branch: "main" (greenfield) | default branch (brownfield)
}

// gitID is the Orion commit identity, so a fresh/unconfigured managed repo can
// commit without relying on the host git config.
var gitID = []string{
	"-c", "user.name=Orion",
	"-c", "user.email=orion@revelara.ai",
	"-c", "commit.gpgsign=false",
}

// git runs `git -C dir args...` (LFS smudge skipped), returning combined output.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_LFS_SKIP_SMUDGE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// isRepo reports whether dir is the working tree of a git repo.
func isRepo(ctx context.Context, dir string) bool {
	out, err := git(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// currentBranch returns the checked-out branch name (the integration base).
func currentBranch(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Resolve returns the project's managed repo, creating it if absent. Idempotent:
// an existing valid repo at the path is reused unchanged.
func Resolve(ctx context.Context, store *contextstore.Store, intake Intake) (Repo, error) {
	path := filepath.Join(store.Dir(), "repo")
	if isRepo(ctx, path) {
		base, err := currentBranch(ctx, path)
		if err != nil {
			return Repo{}, err
		}
		return Repo{Path: path, Base: base}, nil
	}
	return initGreenfield(ctx, path)
}

// initGreenfield creates a fresh managed repo on `main` with one empty initial
// commit, so worktrees have a real base branch to branch off.
func initGreenfield(ctx context.Context, path string) (Repo, error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return Repo{}, fmt.Errorf("mkdir managed repo: %w", err)
	}
	if _, err := git(ctx, path, "init", "-b", "main"); err != nil {
		return Repo{}, err
	}
	if _, err := git(ctx, path, append(append([]string{}, gitID...), "commit", "--allow-empty", "-m", "orion: managed repo init")...); err != nil {
		return Repo{}, err
	}
	return Repo{Path: path, Base: "main"}, nil
}
