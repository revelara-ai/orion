// Package checkpoint is a transparent shadow-git snapshot store (or-ykz.15,
// Hermes checkpoints): it snapshots a worktree before every file-mutating
// turn into a SEPARATE git object store (deduped objects, per-checkpoint
// refs), powering an instant /rollback of a bad turn — finer than the
// branch-level integration rollback, and invisible to the generator (it is
// never registered as a tool; the harness drives it around a turn).
//
// The shadow git dir lives OUTSIDE the worktree, so a rollback's `clean` can
// remove turn-added files without ever touching the checkpoint store itself.
package checkpoint

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Store snapshots one worktree into a shadow git dir.
type Store struct {
	workTree  string
	shadowDir string // the shadow GIT_DIR — never inside workTree
}

// New opens (initializing on first use) a checkpoint store for workTree with
// its object store at shadowDir. shadowDir MUST NOT be inside workTree.
func New(workTree, shadowDir string) (*Store, error) {
	if workTree == "" || shadowDir == "" {
		return nil, fmt.Errorf("checkpoint: workTree and shadowDir are required")
	}
	s := &Store{workTree: workTree, shadowDir: shadowDir}
	if _, err := os.Stat(shadowDir); os.IsNotExist(err) {
		// git init won't create the git-dir's parent chain — do it first.
		if err := os.MkdirAll(filepath.Dir(shadowDir), 0o755); err != nil {
			return nil, fmt.Errorf("checkpoint: shadow dir: %w", err)
		}
		if _, _, err := s.git(context.Background(), "init", "-q"); err != nil {
			return nil, fmt.Errorf("checkpoint: init shadow store: %w", err)
		}
	}
	return s, nil
}

// Checkpoint snapshots the CURRENT worktree and returns a checkpoint id (a
// ref name). Objects dedup against prior checkpoints automatically.
func (s *Store) Checkpoint(ctx context.Context, id string) error {
	if !refNameOK(id) {
		return fmt.Errorf("checkpoint: invalid id %q", id)
	}
	if _, _, err := s.git(ctx, "add", "-A"); err != nil {
		return fmt.Errorf("checkpoint: stage: %w", err)
	}
	tree, _, err := s.git(ctx, "write-tree")
	if err != nil {
		return fmt.Errorf("checkpoint: write-tree: %w", err)
	}
	tree = strings.TrimSpace(tree)
	args := []string{"commit-tree", tree, "-m", "checkpoint " + id}
	// Chain onto the prior checkpoint of the same id so history dedups.
	if prev, _, perr := s.git(ctx, "rev-parse", "--verify", "-q", ref(id)); perr == nil && strings.TrimSpace(prev) != "" {
		args = append(args, "-p", strings.TrimSpace(prev))
	}
	commit, _, err := s.gitEnv(ctx, checkpointEnv(), args...)
	if err != nil {
		return fmt.Errorf("checkpoint: commit-tree: %w", err)
	}
	if _, _, err := s.git(ctx, "update-ref", ref(id), strings.TrimSpace(commit)); err != nil {
		return fmt.Errorf("checkpoint: update-ref: %w", err)
	}
	return nil
}

// Rollback restores the worktree to the EXACT tree captured by checkpoint id:
// tracked files reset, and files added since the checkpoint removed. The
// checkpoint store is untouched (it lives outside the worktree).
func (s *Store) Rollback(ctx context.Context, id string) error {
	if _, _, err := s.git(ctx, "rev-parse", "--verify", "-q", ref(id)); err != nil {
		return fmt.Errorf("checkpoint: no such checkpoint %q", id)
	}
	// read-tree --reset resets the index + tracked worktree files to the tree.
	if _, _, err := s.git(ctx, "read-tree", "-u", "--reset", ref(id)); err != nil {
		return fmt.Errorf("checkpoint: read-tree: %w", err)
	}
	// clean -fd removes worktree files not in the (now-restored) index — the
	// turn-added files. Exact-tree restoration.
	if _, _, err := s.git(ctx, "clean", "-fd"); err != nil {
		return fmt.Errorf("checkpoint: clean: %w", err)
	}
	return nil
}

// Exists reports whether a checkpoint id is stored.
func (s *Store) Exists(ctx context.Context, id string) bool {
	_, _, err := s.git(ctx, "rev-parse", "--verify", "-q", ref(id))
	return err == nil
}

func ref(id string) string { return "refs/checkpoints/" + id }

func refNameOK(id string) bool {
	if id == "" || strings.ContainsAny(id, " ~^:?*[\\") || strings.Contains(id, "..") {
		return false
	}
	return true
}

func checkpointEnv() []string {
	return []string{
		"GIT_AUTHOR_NAME=orion-checkpoint", "GIT_AUTHOR_EMAIL=checkpoint@orion",
		"GIT_COMMITTER_NAME=orion-checkpoint", "GIT_COMMITTER_EMAIL=checkpoint@orion",
	}
}

func (s *Store) git(ctx context.Context, args ...string) (string, string, error) {
	return s.gitEnv(ctx, nil, args...)
}

func (s *Store) gitEnv(ctx context.Context, extraEnv []string, args ...string) (string, string, error) {
	full := append([]string{"--git-dir", s.shadowDir, "--work-tree", s.workTree}, args...)
	cmd := exec.CommandContext(ctx, "git", full...) // #nosec G204 -- fixed binary; git-dir/work-tree are store-owned paths, args are checkpoint plumbing
	cmd.Env = append(os.Environ(), extraEnv...)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		return out.String(), errb.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), errb.String(), nil
}
