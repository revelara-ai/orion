package conductor

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/integration"
	"github.com/revelara-ai/orion/internal/worktree"
)

// WaveReprove proves a merged WAVE at the integration head (or-7et.4): dir is
// the head worktree, wave the clusters just merged, preRev the head rev before
// the wave (for diffing). ok=false rolls the head back to preRev.
type WaveReprove func(ctx context.Context, dir string, wave []decomposer.TaskCluster, preRev string) (bool, error)

// integrateEpic assembles the proven cluster worktrees onto a fresh epic integration head in
// LEASE-DISJOINT WAVES (or-7et.4): clusters whose declared file scopes cannot touch the same
// files merge as one wave and are re-proven ONCE per wave — not once per cluster — with a red
// wave re-proof resetting the head to the pre-wave rev (previously integrated waves survive).
// A mandatory FULL re-proof bookend runs on the final assembled head before the epic verdict,
// so the DELIVERED artifact is still proven whole; a red bookend rejects the epic. Cluster
// worktrees are removed EAGERLY once their wave integrates green (or-7et.4c) instead of at
// end-of-run.
//
// waveReprove nil falls back to the full reprove per wave. With ONE accepted cluster this
// reduces to "commit + fast-forward onto a fresh head" with no re-proof at all — byte-identical
// to the tree that cluster already proved (the or-1lz-reviewed skip).
func integrateEpic(
	ctx context.Context,
	wtMgr *worktree.Manager,
	clusters []decomposer.TaskCluster,
	clusterWT map[string]string,
	results []taskResult,
	base string,
	reprove func(ctx context.Context, dir string) (bool, error),
	waveReprove WaveReprove,
	conform func(cl decomposer.TaskCluster) error,
	onPhase PhaseSink,
) (headDir string, ok bool, err error) {
	accepted := map[string]bool{}
	for _, r := range results {
		if r.Verdict == "Accept" {
			accepted[r.TaskID] = true
		}
	}

	var acceptedClusters []decomposer.TaskCluster
	for _, cl := range clusters {
		if clusterAccepted(cl, accepted) {
			acceptedClusters = append(acceptedClusters, cl)
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
	// Merges never re-prove per cluster — the WAVE re-proof + FULL bookend below own proof cost
	// (or-7et.4a). The integrator still enforces leases + the serialized queue.
	integ := integration.New(intWT.Path, intWT.Branch, nil)
	wtMgr.WithIntegrationCheck(integ.InIntegration) // §6.3: never remove a worktree mid-merge

	// A single accepted cluster fast-forwards onto a fresh head → the integrated tree is IDENTICAL
	// to the tree that cluster already proved, so re-proof is redundant (or-1lz-reviewed skip:
	// scope overlap needs >=2 clusters).
	singleSkip := len(acceptedClusters) <= 1

	for _, wave := range partitionWaves(acceptedClusters) {
		preRev, rerr := gitIn(ctx, headDir, "rev-parse", "HEAD")
		if rerr != nil {
			return headDir, false, fmt.Errorf("read pre-wave rev: %w", rerr)
		}
		preRev = strings.TrimSpace(preRev)

		merged := 0
		for _, cl := range wave {
			// or-7et.5d: the pre-merge conformance gate — an interface mismatch
			// is caught HERE, named per symbol, before the expensive merge +
			// re-proof, not as an epic-wide post-merge failure.
			if conform != nil {
				if cerr := conform(cl); cerr != nil {
					return headDir, false, nil // named + escalated by the gate itself
				}
			}
			wt := clusterWT[cl.Key]
			// or-7et.4c: eager removal (below) or lazy allocation may have left no
			// checkout — the BRANCH survives, so reattach it (or-d3w: never treat a
			// missing checkout as "nothing to integrate"; that silently drops the
			// cluster's files from the head).
			if wt == "" || !dirExists(wt) {
				rewt, rerr := wtMgr.CreateResume(ctx, cl.Key, cl.Key)
				if rerr != nil {
					return headDir, false, fmt.Errorf("reattach worktree for cluster %s: %w", cl.Key, rerr)
				}
				wt = rewt.Path
				clusterWT[cl.Key] = wt
			}
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

			// S1 (or-1lz): the cluster's declared file scope leases the merge; an undeclared scope
			// leases the whole tree. (Within a wave scopes are disjoint by construction, so wave
			// members could merge concurrently; the queue still serializes head advances.)
			out, ierr := integ.Integrate(ctx, cl.Key, wt, cl.Key, clusterLeaseScope(cl))
			if ierr != nil {
				return headDir, false, fmt.Errorf("integrate cluster %s: %w", cl.Key, ierr)
			}
			switch out {
			case integration.Integrated:
				merged++
				onPhase.emit("Integrate", PhaseDone, cl.Key+" merged onto epic head")
			case integration.Conflict:
				onPhase.emit("Integrate", PhaseWarn, cl.Key+": merge conflict — epic not assembled")
				return headDir, false, nil
			case integration.RolledBack:
				onPhase.emit("Integrate", PhaseWarn, cl.Key+": rolled back — epic not assembled")
				return headDir, false, nil
			}
		}

		// Wave re-proof (or-7et.4b): once per wave, scoped by the injected policy
		// (full when nil). Red resets the head to the PRE-WAVE rev — previously
		// integrated waves survive — and the epic is not assembled.
		if merged > 0 && !singleSkip {
			wr := waveReprove
			if wr == nil && reprove != nil {
				wr = func(ctx context.Context, dir string, _ []decomposer.TaskCluster, _ string) (bool, error) {
					return reprove(ctx, dir)
				}
			}
			if wr != nil {
				waveOK, werr := wr(ctx, headDir, wave, preRev)
				if werr != nil || !waveOK {
					if _, rbErr := gitIn(ctx, headDir, "reset", "--hard", preRev); rbErr != nil {
						return headDir, false, fmt.Errorf("wave re-proof failed AND rollback failed: %w", rbErr)
					}
					onPhase.emit("Integrate", PhaseWarn, fmt.Sprintf("wave re-proof RED (%d cluster(s)) — head reset to pre-wave rev", len(wave)))
					return headDir, false, werr
				}
			}
		}

		// or-7et.4c: the wave is green — its cluster worktrees are no longer
		// needed; free the checkouts now instead of holding ~N for the run.
		// (Branches survive; only the disk checkout is dropped.)
		for _, cl := range wave {
			_ = wtMgr.Remove(ctx, cl.Key, worktree.RemoveOpts{Force: true})
			delete(clusterWT, cl.Key)
		}
	}

	// FULL-proof bookend (or-7et.4b): the DELIVERED artifact is proven WHOLE on
	// the final assembled head, whatever the per-wave scoping did. Red rejects
	// the epic. Skipped only for the single-cluster identity case.
	if !singleSkip && reprove != nil {
		bookOK, berr := reprove(ctx, headDir)
		if berr != nil || !bookOK {
			onPhase.emit("Integrate", PhaseWarn, "full-proof bookend RED on the assembled head — epic rejected")
			return headDir, false, berr
		}
		onPhase.emit("Integrate", PhaseDone, "full-proof bookend green on the assembled head")
	}
	return headDir, true, nil
}

// partitionWaves greedily packs accepted clusters into lease-disjoint waves
// (or-7et.4a): a cluster joins the first wave where its scope overlaps no
// member; otherwise it opens a new wave. Undeclared scopes overlap everything
// (the normalizeScope fail-safe), so they always form singleton waves.
func partitionWaves(clusters []decomposer.TaskCluster) [][]decomposer.TaskCluster {
	var waves [][]decomposer.TaskCluster
	var waveScopes [][]string
next:
	for _, cl := range clusters {
		scope := clusterLeaseScope(cl)
		for wi := range waves {
			if !integration.ScopesOverlap(scope, waveScopes[wi]) {
				waves[wi] = append(waves[wi], cl)
				waveScopes[wi] = append(waveScopes[wi], scope...)
				continue next
			}
		}
		waves = append(waves, []decomposer.TaskCluster{cl})
		waveScopes = append(waveScopes, append([]string(nil), scope...))
	}
	return waves
}

// clusterLeaseScope flattens a cluster's declared FileScopes — each entry is a task's FileScope,
// possibly comma-separated path prefixes (decomposer.Task syntax) — into the prefix set the
// integrator leases. Empty (nothing declared) means the integrator leases the whole tree
// (exclusive), the same fail-safe as the dispatch-time leases (leaseSet, dag.go).
func clusterLeaseScope(cl decomposer.TaskCluster) []string {
	var out []string
	for _, fs := range cl.FileScopes {
		out = append(out, leaseSet(fs)...)
	}
	return out
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

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
