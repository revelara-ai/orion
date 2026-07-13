package orchestrator

import (
	"errors"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// TestAmendedSpecReconcilesInsteadOfWedging (or-7et.2 slices 1+2): the intent
// legitimately evolves mid-build — ratify → plan → add_requirement →
// re-approve. The persisted plan was decomposed from the OLD anchor; every
// plan read must now fail LOUD with ErrPlanStale, never silently hand the old
// decomposition to a build that generates+proves against the new hash.
func TestAmendedSpecReconcilesInsteadOfWedging(t *testing.T) {
	c, ctx := storeConductor(t)
	driveToPlan(t, c, ctx)

	if _, err := c.PlanView(ctx); err != nil {
		t.Fatalf("initial plan must read cleanly: %v", err)
	}

	// The developer changes their mind mid-build: a new requirement re-ratifies
	// the spec in place with a NEW hash.
	if err := c.AddRequirement(ctx, spec.Requirement{
		Source: completeness.DimFunctional,
		Text:   "GET /healthz reports ok",
		Cases: []spec.BehavioralCase{{
			Request: spec.RequestShape{Method: "GET", Path: "/healthz"},
			Expect: spec.ExpectShape{Status: 200, ContentType: "application/json",
				Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyPresent, Key: "status"}}},
		}},
	}); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatalf("re-approve after amendment must succeed (amendment is legitimate): %v", err)
	}

	// or-7et.2 slice 2: re-ratification RECONCILES instead of wedging — the plan
	// reads fresh, and (in-place amendment = no diffable lineage) every task is
	// conservatively marked for re-proof.
	pv, err := c.PlanView(ctx)
	if err != nil {
		t.Fatalf("plan read after re-ratification must be RECONCILED fresh, got: %v", err)
	}
	for _, tk := range pv.Tasks {
		if !tk.ReproofRequired {
			t.Fatalf("conservative reconciliation must mark every task, %q kept", tk.Title)
		}
	}
}

// TestStalePlanGuardFiresWithoutReconciliation: the slice-1 guard itself —
// when an epic's anchor mismatches the spec and NO reconciliation ran (here:
// the anchor is corrupted directly), every plan read fails loud.
func TestStalePlanGuardFiresWithoutReconciliation(t *testing.T) {
	c, ctx := storeConductor(t)
	driveToPlan(t, c, ctx)
	if _, err := c.PlanView(ctx); err != nil {
		t.Fatal(err)
	}
	proj, _, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		e, err := tx.Epics().LatestForProject(ctx, proj.ID)
		if err != nil {
			return err
		}
		return tx.Epics().SetSpecHash(ctx, e.ID, "deadbeefdeadbeef")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PlanView(ctx); !errors.Is(err, ErrPlanStale) {
		t.Fatalf("a mismatched anchor with no reconciliation must fail loud, got %v", err)
	}
}

// TestUnamendedReapprovalKeepsPlanFresh: re-approving WITHOUT changing the
// spec (same hash) must not poison the plan — staleness is about the anchor
// moving, not about how many times approve ran.
func TestUnamendedReapprovalKeepsPlanFresh(t *testing.T) {
	c, ctx := storeConductor(t)
	driveToPlan(t, c, ctx)

	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatalf("idempotent re-approve: %v", err)
	}
	if _, err := c.PlanView(ctx); err != nil {
		t.Fatalf("same-hash re-approval must keep the plan readable: %v", err)
	}
}

// TestLegacyEpicWithoutHashIsGrandfathered: epics created before the
// spec_hash migration carry ” — they must keep reading (an additive
// migration must not brick existing projects); only a RECORDED hash that
// mismatches is stale.
func TestLegacyEpicWithoutHashIsGrandfathered(t *testing.T) {
	c, ctx := storeConductor(t)
	driveToPlan(t, c, ctx)
	if _, err := c.PlanView(ctx); err != nil { // materialize the epic (decompose-on-demand)
		t.Fatalf("plan: %v", err)
	}

	// Simulate a pre-migration epic: blank out the recorded hash (SetSpecHash is
	// also the slice-2 reconciliation primitive that re-stamps after invalidation).
	proj, _, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		e, err := tx.Epics().LatestForProject(ctx, proj.ID)
		if err != nil {
			return err
		}
		return tx.Epics().SetSpecHash(ctx, e.ID, "")
	}); err != nil {
		t.Fatalf("blank hash: %v", err)
	}
	if _, err := c.PlanView(ctx); err != nil {
		t.Fatalf("legacy epic (no recorded hash) must be grandfathered, got: %v", err)
	}
}
