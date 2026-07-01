package conductor

import (
	"context"
	"fmt"

	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/integration"
	"github.com/revelara-ai/orion/internal/worktree"
)

// integrateEpic merges the proven cluster worktrees ONE AT A TIME onto a fresh epic integration
// head (or-tcs.1.5/.1.6), re-proving the assembled tree after each merge — so the DELIVERED
// artifact is the integrated whole, not one cluster's tree. Each accepted cluster's generated
// build is committed on its branch (= cluster key) first; a merge conflict or a red post-merge
// re-proof fails the epic (ok=false). The integrator's mid-integration predicate is wired into the
// worktree manager so a cluster worktree is never removed out from under its merge (§6.3).
//
// Returns the integration head dir (the single delivery artifact) and whether every accepted
// cluster integrated cleanly. With ONE accepted cluster this reduces to "commit + fast-forward the
// cluster onto a fresh head" — behaviourally the same tree the single-cluster build delivered.
func integrateEpic(
	ctx context.Context,
	wtMgr *worktree.Manager,
	clusters []decomposer.TaskCluster,
	clusterWT map[string]string,
	results []taskResult,
	base string,
	reprove func(ctx context.Context, dir string) (bool, error),
	onPhase PhaseSink,
) (headDir string, ok bool, err error) {
	accepted := map[string]bool{}
	for _, r := range results {
		if r.Verdict == "Accept" {
			accepted[r.TaskID] = true
		}
	}

	nAccepted := 0
	for _, cl := range clusters {
		if clusterAccepted(cl, accepted) {
			nAccepted++
		}
	}

	// Recreate (not Create): a prior `orion run` on this repo may have left the epic-integration
	// worktree+branch; the head must be FRESH from base each run, so clear any stale one first —
	// otherwise a re-run crashes on "a branch named epic-integration already exists" (or-d3w).
	intWT, err := wtMgr.Recreate(ctx, "epic-integration", base)
	if err != nil {
		return "", false, fmt.Errorf("integration head worktree: %w", err)
	}
	headDir = intWT.Path
	// A single accepted cluster fast-forwards onto a fresh head → the integrated tree is IDENTICAL
	// to the tree that cluster already proved, so the per-merge re-proof is redundant (and doubles
	// proof cost). Re-prove only a non-trivial (>1 cluster) ASSEMBLY; the structural wireup gate
	// (or-tcs.3) still runs on the integrated tree either way.
	effReprove := reprove
	if nAccepted <= 1 {
		effReprove = nil
	}
	integ := integration.New(intWT.Path, intWT.Branch, effReprove)
	wtMgr.WithIntegrationCheck(integ.InIntegration) // §6.3: never remove a worktree mid-merge

	for _, cl := range clusters {
		if !clusterAccepted(cl, accepted) {
			continue // a cluster with a non-accepted task is not integrated (the epic will Reject)
		}
		wt := clusterWT[cl.Key]
		// Commit any uncommitted build on the cluster's branch (= cl.Key) so it is a mergeable ref.
		// A re-run's cluster is ALREADY committed (clean worktree) — do NOT mistake that for "no
		// change" and skip it, or the re-assembled head loses the cluster's files (or-d3w).
		if status, _ := gitIn(ctx, wt, "status", "--porcelain"); status != "" {
			if _, e := gitIn(ctx, wt, "add", "-A"); e != nil {
				return headDir, false, e
			}
			if _, e := gitIn(ctx, wt,
				"-c", "user.name=Orion", "-c", "user.email=orion@revelara.ai", "-c", "commit.gpgsign=false",
				"commit", "--no-verify", "-m", "orion: cluster "+cl.Key); e != nil {
				return headDir, false, e
			}
		}
		// Skip only a GENUINELY empty cluster — its branch has no commits beyond base to integrate.
		if ahead, _ := gitIn(ctx, wt, "rev-list", "--count", base+"..HEAD"); ahead == "0" {
			continue
		}

		out, ierr := integ.Integrate(ctx, cl.Key, wt, cl.Key)
		if ierr != nil {
			return headDir, false, fmt.Errorf("integrate cluster %s: %w", cl.Key, ierr)
		}
		switch out {
		case integration.Integrated:
			onPhase.emit("Integrate", PhaseDone, cl.Key+" merged onto epic head")
		case integration.Conflict:
			onPhase.emit("Integrate", PhaseWarn, cl.Key+": merge conflict — epic not assembled")
			return headDir, false, nil
		case integration.RolledBack:
			onPhase.emit("Integrate", PhaseWarn, cl.Key+": post-merge re-proof RED — rolled back")
			return headDir, false, nil
		}
	}
	return headDir, true, nil
}

// clusterAccepted reports whether every member task of the cluster reached Accept.
func clusterAccepted(cl decomposer.TaskCluster, accepted map[string]bool) bool {
	for _, m := range cl.Members {
		if !accepted[m] {
			return false
		}
	}
	return len(cl.Members) > 0
}
