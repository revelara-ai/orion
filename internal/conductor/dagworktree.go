package conductor

import (
	"context"
	"fmt"
	"sync"

	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/worktree"
)

// lazyWorktrees (or-7et.4c) allocates cluster worktrees AT DISPATCH instead of
// upfront: an N-cluster plan holds ~maxConc checkouts, not N. Each cluster's
// generated code is built inside its own isolated, scoped workdir — the
// agent's only writable directory (or-tcs.1.3). The shared map is what
// integrateEpic consumes (and eagerly empties as waves integrate); cleanup
// removes whatever remains at end of run. Safe for parallel dispatch.
func lazyWorktrees(ctx context.Context, mgr *worktree.Manager, base string) (paths map[string]string, get func(key string) (string, error), cleanup func()) {
	paths = map[string]string{}
	var mu sync.Mutex
	get = func(key string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if key == "" {
			return "", fmt.Errorf("task has no cluster")
		}
		if p, ok := paths[key]; ok {
			return p, nil
		}
		// Recreate (not Create): each cluster tree holds freshly-GENERATED code built from base, so
		// it must be fresh each run — and a prior `orion run` leaves the key branch behind (Remove
		// drops the worktree, not the branch), which plain Create would collide on (or-d3w).
		wt, cerr := mgr.Recreate(ctx, key, base)
		if cerr != nil {
			return "", cerr
		}
		paths[key] = wt.Path
		return wt.Path, nil
	}
	cleanup = func() {
		mu.Lock()
		defer mu.Unlock()
		for k := range paths {
			_ = mgr.Remove(ctx, k, worktree.RemoveOpts{Force: true})
			delete(paths, k)
		}
	}
	return paths, get, cleanup
}

// singleCluster collapses all tasks into one cluster — the fallback used when
// clustering cannot be computed, so the build still runs in a single isolated
// worktree rather than failing.
func singleCluster(tasks []orchestrator.PlanTask) decomposer.TaskCluster {
	c := decomposer.TaskCluster{Key: "all"}
	for _, t := range tasks {
		c.Members = append(c.Members, t.ID)
	}
	return c
}
