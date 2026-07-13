package conductor

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/revelara-ai/orion/internal/actuation"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/worktree"
)

// Post-proof actions as policy, not re-prompting (or-7fd): once a change is
// PROVEN (do-no-harm held + ratified oracle passed), landing it ff-only,
// closing the issue it resolved, and reclaiming the branch/worktree are
// mechanical — the proof was the gate. One workflow does all three:
//   - opted in (ORION_POST_PROOF=auto, red button clear): runs automatically
//     after the proof, no per-change prompt;
//   - not opted in: the finish_change tool runs it after a SINGLE consolidated
//     confirm (one destructive tool call = one approval), never two rounds.

// postProofAutonomy reports the developer's standing opt-in. or-v9f.30 (the
// earned-autonomy ladder) will feed this from delivery track record; today it
// is an explicit env opt-in.
func postProofAutonomy() bool { return os.Getenv("ORION_POST_PROOF") == "auto" }

// LandProvenChange is the consolidated post-proof workflow for a PROVEN,
// committed change branch: fast-forward it onto the developer's checked-out
// base, close the beads issue the change cites (only when it cites exactly
// one, verified readable), and reclaim the worktree + branch. Returns a
// human-readable summary of what happened.
//
// The ff-only stance is load-bearing: if the base moved since the proof, the
// proof is STALE and landing must refuse — the caller re-runs the change flow
// off current HEAD rather than hand-merging unproven state.
func LandProvenChange(ctx context.Context, repoRoot string, store *contextstore.Store, rb actuation.RedButton, branch, intent string) (string, error) {
	if strings.TrimSpace(branch) == "" {
		return "", fmt.Errorf("no review branch to land")
	}
	if err := rb.Guard("land proven change"); err != nil {
		return "", err
	}
	if out, err := gitIn(ctx, repoRoot, "merge", "--ff-only", branch); err != nil {
		return "", fmt.Errorf("not landed: %s is not a fast-forward of the current base — the base moved since the change was proven, so the proof is STALE. Re-run the change flow off current HEAD (it re-proves against the real state), then land the fresh branch. (git: %s)", branch, firstLine(out))
	}
	var steps []string
	steps = append(steps, "landed "+branch+" (fast-forward)")

	// Close the cited issue — only under the exactly-one rule, and only after
	// bd can actually read it (closing the WRONG issue is worse than leaving
	// one open).
	if id := citedIssue(ctx, repoRoot, intent); id != "" {
		if out, exit := bdRun(ctx, repoRoot, "close", id, "--reason", "landed by orion post-proof ("+branch+")"); exit == 0 {
			steps = append(steps, "closed "+id)
		} else {
			steps = append(steps, "issue "+id+" not closed: "+firstLine(out))
		}
	}

	// Reclaim the worktree + branch: after an ff-land the branch is merged
	// state, and the worktree exists only to host the review.
	if err := worktree.New(repoRoot, store).RemoveWithBranch(ctx, branch, worktree.RemoveOpts{Force: true}); err != nil {
		// No worktree (already reclaimed, or the branch was cut without one):
		// the branch is merged state now — delete it directly, -d stays safe.
		if out, derr := gitIn(ctx, repoRoot, "branch", "-d", branch); derr != nil && !strings.Contains(out, "not found") {
			steps = append(steps, "cleanup incomplete: "+firstLine(out))
		} else {
			steps = append(steps, "reclaimed branch")
		}
	} else {
		steps = append(steps, "reclaimed worktree + branch")
	}
	return strings.Join(steps, "; "), nil
}

// issueRefRE matches beads-style issue ids (prefix-token, optional .N child).
var issueRefRE = regexp.MustCompile(`\b[a-z]{1,8}-[a-z0-9]{2,8}(?:\.[0-9]{1,3})?\b`)

// citedIssue returns the ONE beads issue the intent cites, or "". Two or more
// distinct ids is ambiguity — refuse to guess. The id must be readable in the
// repo's beads workspace before it counts.
func citedIssue(ctx context.Context, repoRoot, intent string) string {
	if _, ok := beadsWorkspace(ctx); !ok {
		return ""
	}
	distinct := map[string]bool{}
	for _, m := range issueRefRE.FindAllString(intent, -1) {
		distinct[m] = true
	}
	if len(distinct) != 1 {
		return ""
	}
	var id string
	for k := range distinct {
		id = k
	}
	if _, exit := bdRun(ctx, repoRoot, "show", id, "--json"); exit != 0 {
		return "" // unreadable → not a real issue here; do not close blind
	}
	return id
}
