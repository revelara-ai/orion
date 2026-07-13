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

// gitID returns the Orion commit identity, so a fresh/unconfigured managed repo
// can commit without relying on the host git config. It returns a FRESH slice on
// every call so a caller appending to (or mutating) the result can never corrupt
// the shared identity args.
func gitID() []string {
	return []string{
		"-c", "user.name=Orion",
		"-c", "user.email=orion@revelara.ai",
		"-c", "commit.gpgsign=false",
	}
}

// git runs `git -C dir args...` (LFS smudge skipped), returning combined output.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...) // #nosec G204 -- fixed binary; harness-built args
	cmd.Env = append(os.Environ(), "GIT_LFS_SKIP_SMUDGE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// repoState classifies the git status of a candidate managed-repo path, so the
// absent-vs-corrupt distinction is not swallowed: a genuinely-absent path is
// init/clone-able, but a present-but-corrupt repo must be surfaced, never
// init-ed over.
type repoState int

const (
	stateAbsent        repoState = iota // no repo present — safe to init/clone
	stateValidWorkTree                  // a healthy git work tree — reuse it
	stateCorrupt                        // a .git is present but git can't read it — surface, don't overwrite
)

// String renders the classification (also the value the proof oracle asserts on).
func (s repoState) String() string {
	switch s {
	case stateValidWorkTree:
		return "valid-work-tree"
	case stateCorrupt:
		return "corrupt"
	default:
		return "absent"
	}
}

// classify determines whether dir is a valid work tree, genuinely absent, or a
// present-but-corrupt repo. git treats an empty/partial .git the same as absent
// ("not a git repository"), so we disambiguate: if rev-parse fails but a .git
// entry exists at the path, the repo is corrupt rather than absent.
func classify(ctx context.Context, dir string) repoState {
	out, err := git(ctx, dir, "rev-parse", "--is-inside-work-tree")
	if err == nil && strings.TrimSpace(out) == "true" {
		return stateValidWorkTree
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr == nil {
		return stateCorrupt
	}
	return stateAbsent
}

// isRepo reports whether dir is the working tree of a git repo.
func isRepo(ctx context.Context, dir string) bool {
	return classify(ctx, dir) == stateValidWorkTree
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
	switch classify(ctx, path) {
	case stateValidWorkTree:
		base, err := currentBranch(ctx, path)
		if err != nil {
			return Repo{}, err
		}
		return Repo{Path: path, Base: base}, nil
	case stateCorrupt:
		// A .git is present but git can't read it — a partial/corrupt repo. Surface
		// it clearly instead of git-init-ing over the corruption with an opaque error.
		return Repo{}, fmt.Errorf("managed repo at %s is a corrupt/invalid git repository; remove or repair it before retrying", path)
	}
	// stateAbsent — safe to seed.
	if intake.Brownfield {
		return cloneBrownfield(ctx, path, intake.Source)
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
	if _, err := git(ctx, path, append(gitID(), "commit", "--allow-empty", "-m", "orion: managed repo init")...); err != nil {
		return Repo{}, err
	}
	return Repo{Path: path, Base: "main"}, nil
}

// cloneBrownfield clones the target repo into the managed path; Base is the
// cloned default branch.
func cloneBrownfield(ctx context.Context, path, source string) (Repo, error) {
	if strings.TrimSpace(source) == "" {
		return Repo{}, fmt.Errorf("brownfield intake requires a Source repo")
	}
	// Clone from the managed repo's PARENT dir (which exists), not the process cwd —
	// a cwd anchor (git -C '.') breaks when the cwd is a since-removed worktree.
	if _, err := git(ctx, filepath.Dir(path), "clone", source, path); err != nil {
		return Repo{}, err
	}
	base, err := currentBranch(ctx, path)
	if err != nil {
		return Repo{}, err
	}
	return Repo{Path: path, Base: base}, nil
}
