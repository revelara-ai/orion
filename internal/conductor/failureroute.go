package conductor

import (
	"context"
	"sync"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// Cross-step backward-edge failure routing (or-tcs.4): the workflow is
// linear with local loops, but real failures cascade backward — a step-8
// "code keeps failing proof" can be a step-5 spec defect. The within-task
// refinement loop already retries code; this diagnoses WHICH earlier step
// an exhausted task points at and routes there, so a contradictory contract
// re-opens the SPEC instead of dead-ending as a delivery-framed escalation.

// classifyFailureStep diagnoses the at-fault step from the attempt
// trajectory (passing-obligation count per failed attempt). Deterministic:
// ZERO progress across >=2 causal-analysis-informed refinements means
// re-coding is not converging on the contract — the defect sits at the spec
// (step 5). Any movement → code-level (step 8): the loop was converging or
// at least searching. Decomposition-level (step 6) diagnosis is a named
// residual (needs cross-task surface evidence).
func classifyFailureStep(passTrajectory []int) string {
	if len(passTrajectory) < 2 {
		return "code"
	}
	first := passTrajectory[0]
	for _, p := range passTrajectory[1:] {
		if p != first {
			return "code"
		}
	}
	return "spec"
}

// reopenSpecForDefect files the backward edge: an amendment DRAFT off the
// project's latest accepted spec (lineage preserved — the accepted spec
// stays the anchor for the current epic's record). Returns the draft id,
// "" on any miss (best-effort: the escalation still carries the diagnosis).
func reopenSpecForDefect(ctx context.Context, store *contextstore.Store, stateMu *sync.Mutex, projectID string) string {
	var draftID string
	withLock(stateMu, func() {
		_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
			sp, err := tx.Specs().LatestForProject(ctx, projectID)
			if err != nil || sp.Status != "accepted" {
				return err
			}
			id, err := tx.Specs().CreateAmendmentDraft(ctx, sp)
			if err == nil {
				draftID = id
			}
			return err
		})
	})
	return draftID
}
