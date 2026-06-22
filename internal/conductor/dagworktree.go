package conductor

import (
	"context"
	"fmt"

	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/worktree"
)

// clusterWorktreeSet creates one git worktree per cluster off the project repo's
// base, returning a map of cluster key → worktree path plus a cleanup func that
// removes every worktree it created. Each cluster's generated code is built inside
// its own isolated, scoped workdir — the agent's only writable directory — which is
// the foundation for parallel cluster builds and conflict-free integration
// (or-tcs.1.3, PRD side-effect sandboxing / trust-domain isolation). On any failure
// the partially-created worktrees are cleaned up before returning.
func clusterWorktreeSet(ctx context.Context, mgr *worktree.Manager, clusters []decomposer.TaskCluster, base string) (paths map[string]string, cleanup func(), err error) {
	paths = map[string]string{}
	created := make([]string, 0, len(clusters))
	cleanup = func() {
		for _, k := range created {
			_ = mgr.Remove(ctx, k, worktree.RemoveOpts{Force: true})
		}
	}
	for _, cl := range clusters {
		wt, cerr := mgr.Create(ctx, cl.Key, base)
		if cerr != nil {
			cleanup()
			return nil, nil, fmt.Errorf("worktree for cluster %s: %w", cl.Key, cerr)
		}
		paths[cl.Key] = wt.Path
		created = append(created, cl.Key)
	}
	return paths, cleanup, nil
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
