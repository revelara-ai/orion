// Package integration is the serialized merge queue (or-tcs.1.5, PRD Phase E2): proven cluster
// branches integrate ONE AT A TIME onto the epic integration head. Each integration rebases the
// cluster branch onto the current head, fast-forwards it into the head, RE-PROVES the merged tree,
// and advances the head only if the proof is green — otherwise it rolls the head back. File-scope
// path leases keep two clusters from racing on the same files; a single queue lock serializes the
// merges. This is the layer that turns "each cluster proven in isolation" into "the clusters
// actually assemble + still prove."
//
// Crash-recovery persistence (resuming an interrupted integration across process restarts via the
// Context Store) is tracked separately; within one build process the in-memory queue lock + leases
// are the source of truth and the git head is durable.
package integration

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// Outcome is the result of integrating one cluster onto the head.
type Outcome string

const (
	Integrated Outcome = "integrated"  // rebased, merged, re-proven green; head advanced
	Conflict   Outcome = "conflict"    // rebase hit a merge conflict; head untouched
	RolledBack Outcome = "rolled_back" // merged but the re-proof was red; head reset to before
)

// Integrator serializes merges of proven cluster branches onto an integration head and re-proves
// the merged tree. Safe for concurrent callers: Integrate is serialized by the queue lock, and the
// lease/active maps are mutex-guarded.
type Integrator struct {
	headDir string // the integration worktree, checked out on the head branch
	headRef string // the head branch name (rebase target)
	// reprove proves the merged tree at dir; a red (ok=false) result rolls the head back. Injected
	// so the integrator stays independent of the proof domain.
	reprove func(ctx context.Context, dir string) (ok bool, err error)

	queue sync.Mutex // singleton lock: one integration at a time (the serialized queue)

	mu     sync.Mutex          // guards leases + active
	leases map[string][]string // taskID -> file-scope prefixes it holds
	active map[string]bool     // taskID -> mid-integration (in the queue / merging)
}

// New returns an Integrator over an integration worktree checked out on headRef. reprove proves
// the merged tree (nil → integrations are accepted without a re-proof, for tests that don't need it).
func New(headDir, headRef string, reprove func(ctx context.Context, dir string) (bool, error)) *Integrator {
	return &Integrator{
		headDir: headDir, headRef: headRef, reprove: reprove,
		leases: map[string][]string{}, active: map[string]bool{},
	}
}

// AcquireLease declares taskID's file scope (path prefixes). It fails if any prefix OVERLAPS a
// lease another task already holds — the collision-avoidance gate, so two clusters never edit the
// same files concurrently. Re-acquiring for the same task replaces its scope.
func (i *Integrator) AcquireLease(taskID string, scope []string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	for other, held := range i.leases {
		if other == taskID {
			continue
		}
		for _, a := range scope {
			for _, b := range held {
				if pathsOverlap(a, b) {
					return fmt.Errorf("path lease conflict: %q overlaps %q held by %s", a, b, other)
				}
			}
		}
	}
	i.leases[taskID] = append([]string(nil), scope...)
	return nil
}

// ReleaseLease drops taskID's lease (call after the task integrates or is abandoned).
func (i *Integrator) ReleaseLease(taskID string) {
	i.mu.Lock()
	delete(i.leases, taskID)
	i.mu.Unlock()
}

// InIntegration reports whether taskID is mid-integration — the predicate for
// worktree.Manager.WithIntegrationCheck (§6.3: never remove a worktree out from under a merge).
func (i *Integrator) InIntegration(taskID string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.active[taskID]
}

// Integrate merges the proven cluster at clusterDir (branch) onto the integration head, ONE AT A
// TIME: rebase the cluster branch onto the head, fast-forward it into the head, re-prove the merged
// tree, and advance the head iff green — else reset the head to where it was (rolled back). A rebase
// conflict leaves the head untouched (Conflict).
func (i *Integrator) Integrate(ctx context.Context, taskID, clusterDir, branch string) (Outcome, error) {
	i.queue.Lock() // serialized integration queue: exactly one merge in flight
	defer i.queue.Unlock()
	i.setActive(taskID, true)
	defer i.setActive(taskID, false)

	headBefore, err := i.headRev(ctx)
	if err != nil {
		return "", err
	}

	// Rebase the cluster branch onto the current head (in the cluster's own worktree). A conflict
	// here is the file-scope collision the leases aim to prevent; abort and report it.
	if _, code, rerr := gitAt(ctx, clusterDir, "rebase", i.headRef); rerr != nil || code != 0 {
		_, _, _ = gitAt(ctx, clusterDir, "rebase", "--abort")
		return Conflict, nil
	}

	// The cluster branch now descends from head → fast-forward it into the head.
	if _, code, ferr := gitAt(ctx, i.headDir, "merge", "--ff-only", branch); ferr != nil || code != 0 {
		return Conflict, nil // shouldn't happen after a clean rebase; treat defensively
	}

	// RE-PROVE the merged tree: per-cluster proofs do not imply the assembled tree is correct.
	if i.reprove != nil {
		ok, perr := i.reprove(ctx, i.headDir)
		if perr != nil || !ok {
			// Red (or errored) post-merge proof → roll the head back to before this integration.
			if _, _, rbErr := gitAt(ctx, i.headDir, "reset", "--hard", headBefore); rbErr != nil {
				return RolledBack, fmt.Errorf("re-proof failed AND rollback failed: %w", rbErr)
			}
			return RolledBack, perr
		}
	}
	return Integrated, nil
}

func (i *Integrator) setActive(taskID string, v bool) {
	i.mu.Lock()
	if v {
		i.active[taskID] = true
	} else {
		delete(i.active, taskID)
	}
	i.mu.Unlock()
}

func (i *Integrator) headRev(ctx context.Context) (string, error) {
	out, code, err := gitAt(ctx, i.headDir, "rev-parse", "HEAD")
	if err != nil || code != 0 {
		return "", fmt.Errorf("read head rev: %v (exit %d)", err, code)
	}
	return strings.TrimSpace(out), nil
}

// pathsOverlap reports whether two declared file-scope prefixes can touch the same files — true
// when one is a path-prefix of the other (directory-prefix scoping).
func pathsOverlap(a, b string) bool {
	a, b = cleanPrefix(a), cleanPrefix(b)
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func cleanPrefix(p string) string {
	return strings.TrimRight(strings.TrimSpace(p), "/")
}

// gitAt runs `git -C dir args...` and returns combined output, exit code, and a launch error
// (nil for a clean run or a non-zero exit; non-nil only when git couldn't start).
func gitAt(ctx context.Context, dir string, args ...string) (string, int, error) {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err == nil {
		return string(out), 0, nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode(), nil
	}
	return string(out), -1, err
}
