// Package conductor is the per-task verification-gated state machine (or-60u, PRD
// Phase E13/E16). It drives a task through ready → in_progress → being_validated
// → proven/done, and a task can ONLY reach proven/done with a proof whose verdict
// is Accept. The gate is enforced at the DB layer (contextstore done-gate
// trigger); this state machine records the harness verdict and invokes the
// transition — it never fabricates a verdict.
//
// Manifesto: closure is verification-gated; no agent grades its own homework.
package conductor

import (
	"context"
	"fmt"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// StateMachine drives task status transitions against the Context Store.
type StateMachine struct {
	store *contextstore.Store
}

// New returns a state machine bound to the Context Store.
func New(store *contextstore.Store) *StateMachine { return &StateMachine{store: store} }

// Begin marks a task in_progress.
func (sm *StateMachine) Begin(ctx context.Context, taskID string) error {
	return sm.setStatus(ctx, taskID, "in_progress")
}

// Validate marks a task being_validated (proof is running).
func (sm *StateMachine) Validate(ctx context.Context, taskID string) error {
	return sm.setStatus(ctx, taskID, "being_validated")
}

func (sm *StateMachine) setStatus(ctx context.Context, taskID, status string) error {
	return sm.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.Tasks().SetStatus(ctx, taskID, status)
	})
}

// RecordVerdict persists the converged proof as a proofs row (harness-collected
// evidence) and returns its id. The verdict string comes from truthalign, never
// from an agent.
func (sm *StateMachine) RecordVerdict(ctx context.Context, taskID string, o truthalign.Outcome) (string, error) {
	mode := "behavioral"
	if len(o.Modes) == 1 {
		mode = o.Modes[0].Mode
	}
	var metrics map[string]float64
	if len(o.Modes) > 0 {
		metrics = o.Modes[0].Metrics
	}
	var proofID string
	err := sm.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		proofID, e = tx.Proofs().Create(ctx, taskID, contextstore.Proof{
			Mode:     mode,
			Verdict:  string(o.Verdict),
			RunCount: int(metrics["run_count"]),
		})
		return e
	})
	return proofID, err
}

// Close transitions a task to done. The DB done-gate rejects the transition
// unless proofID references a proof with verdict=Accept — so a Reject/Inconclusive
// proof (or no proof) cannot close the task.
func (sm *StateMachine) Close(ctx context.Context, taskID, proofID string) error {
	if proofID == "" {
		return fmt.Errorf("close %s: no proof recorded (verification-gated)", taskID)
	}
	return sm.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.Tasks().SetProofAndStatus(ctx, taskID, proofID, "done")
	})
}

// ProveAndClose records the verdict and, only if it is Accept, closes the task.
// Returns whether the task was closed.
func (sm *StateMachine) ProveAndClose(ctx context.Context, taskID string, o truthalign.Outcome) (bool, error) {
	proofID, err := sm.RecordVerdict(ctx, taskID, o)
	if err != nil {
		return false, err
	}
	if o.Verdict != truthalign.Accept {
		return false, nil // not proven — task stays open for the loop to remediate
	}
	if err := sm.Close(ctx, taskID, proofID); err != nil {
		return false, err
	}
	return true, nil
}
