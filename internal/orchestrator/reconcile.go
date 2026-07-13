package orchestrator

import (
	"context"

	"fmt"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// ReconcileView reports how an amended anchor was reconciled into a fresh plan
// (or-7et.2 slices 2+3).
type ReconcileView struct {
	EpicID       string   `json:"epic_id"`
	Conservative bool     `json:"conservative"` // no diffable lineage — every task re-proves
	Dirty        []string `json:"dirty"`        // changed spec surface (case ids)
	Invalidated  []string `json:"invalidated"`  // task titles marked reproof_required
	Kept         []string `json:"kept"`         // task titles eligible for memo reuse
}

// ReconcilePlan re-plans after a spec amendment (or-7et.2 slice 2): it
// decomposes the NEW anchor, marks the tasks whose COVERED slice changed for
// mandatory re-proof, re-keys the proof memos so byte-identical artifacts keep
// their verdicts, and records a realignment event (slice 3).
//
// Invalidation is covers∩dirty, deliberately WITHOUT transitive dependent
// propagation: a dependent task's memo reuse is sound by construction — reuse
// requires its obligations unchanged (covers untouched) AND a byte-identical
// artifact (content hash), and a dependency change that alters its bytes
// misses the memo naturally. Composition effects are the assembly re-proof's
// job, which always re-runs. Conservative mode (no diffable lineage — the
// ratified row was mutated in place) marks EVERY task: correctness first.
func (c *Conductor) ReconcilePlan(ctx context.Context) (ReconcileView, error) {
	if c.store == nil {
		return ReconcileView{}, errNoStore
	}
	proj, sp, err := c.currentProjectSpec(ctx)
	if err != nil {
		return ReconcileView{}, err
	}
	if sp.Status != "accepted" {
		return ReconcileView{}, fmt.Errorf("reconcile: the current spec is not ratified (status=%s)", sp.Status)
	}
	epic, err := c.currentEpic(ctx, proj.ID)
	if err != nil {
		return ReconcileView{}, fmt.Errorf("reconcile: no plan to reconcile: %w", err)
	}
	if epic.SpecHash == "" || epic.SpecHash == sp.Hash {
		return ReconcileView{EpicID: epic.ID}, nil // current (or grandfathered) — nothing to do
	}

	// The OLD surface: the spec row the stale epic was decomposed from. If that
	// row's hash no longer matches the epic's anchor, it was re-ratified IN
	// PLACE — the old requirements are gone, nothing can be proven unchanged.
	var oldSp contextstore.Spec
	if err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		s, e := tx.Specs().Get(ctx, epic.SpecID)
		oldSp = s
		return e
	}); err != nil {
		return ReconcileView{}, err
	}
	conservative := oldSp.Hash != epic.SpecHash
	var dirty map[string]bool
	if !conservative {
		dirty = dirtyCaseIDs(oldSp.Requirements, sp.Requirements)
	}

	// Decompose the NEW anchor (deterministic oracle) and gate coverage.
	es, err := c.RecallSpec(ctx)
	if err != nil {
		return ReconcileView{}, err
	}
	newEpic := decomposer.Decompose(es, c.gate.ProjectType())
	if err := decomposer.CoverageGate(es, newEpic); err != nil {
		return ReconcileView{}, fmt.Errorf("reconcile: coverage gate on the amended spec: %w", err)
	}

	rv := ReconcileView{Conservative: conservative, Dirty: sortedKeys(dirty)}
	affected := map[string]bool{}
	for _, t := range newEpic.Tasks {
		hit := conservative
		for _, cov := range t.Covers {
			if dirty[cov] {
				hit = true
				break
			}
		}
		affected[t.Key] = hit
		if hit {
			rv.Invalidated = append(rv.Invalidated, t.Title)
		} else {
			rv.Kept = append(rv.Kept, t.Title)
		}
	}

	err = c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		eid, keyToID, e := persistEpicTx(ctx, tx, proj.ID, sp.ID, sp.Hash, newEpic)
		if e != nil {
			return e
		}
		rv.EpicID = eid
		for key, hit := range affected {
			if hit {
				if e := tx.Tasks().SetReproofRequired(ctx, keyToID[key], true); e != nil {
					return e
				}
			}
		}
		// Slice 3: the realignment event — the human-visible record of what the
		// amendment changed and what it cost.
		detail := fmt.Sprintf("spec %.12s → %.12s (conservative=%v)\nchanged surface: %s\ninvalidated (re-prove): %s\nkept (memo-eligible): %s",
			epic.SpecHash, sp.Hash, conservative,
			orNone(strings.Join(rv.Dirty, ", ")), orNone(strings.Join(rv.Invalidated, "; ")), orNone(strings.Join(rv.Kept, "; ")))
		_, e = tx.Escalations().CreateDetailed(ctx, proj.ID, "", "realignment: plan reconciled to the amended spec", detail)
		return e
	})
	if err != nil {
		return ReconcileView{}, err
	}
	// Re-key the proof memos AFTER the plan write (separate tx — the store holds
	// one connection; nesting would deadlock). Byte-identical artifacts of KEPT
	// tasks reuse their verdicts; invalidated tasks bypass the memo regardless.
	if err := c.store.CopyProofMemos(ctx, epic.SpecHash, sp.Hash); err != nil {
		c.log.WarnContext(ctx, "reconcile: memo re-key failed (kept tasks will re-prove)", "err", err)
	}
	c.log.InfoContext(ctx, "plan reconciled to the amended spec",
		"epic_id", rv.EpicID, "conservative", conservative,
		"invalidated", len(rv.Invalidated), "kept", len(rv.Kept))
	return rv, nil
}

// dirtyCaseIDs diffs two requirements JSONs by content-addressed requirement
// ID: cases of added/removed requirements are the changed surface. (A changed
// requirement IS a remove+add — IDs are content-addressed.)
func dirtyCaseIDs(oldJSON, newJSON string) map[string]bool {
	oldReqs, newReqs := loadRequirements(oldJSON), loadRequirements(newJSON)
	oldByID, newByID := map[string]spec.Requirement{}, map[string]spec.Requirement{}
	for _, r := range oldReqs {
		oldByID[r.ID] = r
	}
	for _, r := range newReqs {
		newByID[r.ID] = r
	}
	dirty := map[string]bool{}
	mark := func(r spec.Requirement) {
		for _, cse := range r.Cases {
			if cse.ID != "" {
				dirty[cse.ID] = true
			}
		}
	}
	for id, r := range oldByID {
		if _, ok := newByID[id]; !ok {
			mark(r)
		}
	}
	for id, r := range newByID {
		if _, ok := oldByID[id]; !ok {
			mark(r)
		}
	}
	return dirty
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// reconcileAfterRatify is the ApproveSpec hook (or-7et.2): when an epic exists
// and the freshly-ratified anchor outdates it, reconcile immediately so the
// developer lands on a fresh plan instead of a stale-plan wall. Best-effort:
// on failure the loud ErrPlanStale guards keep protecting every read.
func (c *Conductor) reconcileAfterRatify(ctx context.Context, projID, newHash string) {
	epic, err := c.currentEpic(ctx, projID)
	if err != nil || epic.SpecHash == "" || epic.SpecHash == newHash {
		return
	}
	if _, err := c.ReconcilePlan(ctx); err != nil {
		c.log.WarnContext(ctx, "re-ratification left the plan STALE (reconcile failed) — plan reads fail loud until resolved", "err", err)
	}
}
