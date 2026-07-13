package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

func latestSpecRow(t *testing.T, c *Conductor, ctx context.Context) contextstore.Spec {
	t.Helper()
	proj, _, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sp contextstore.Spec
	if err := c.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		s, e := tx.Specs().LatestForProject(ctx, proj.ID)
		sp = s
		return e
	}); err != nil {
		t.Fatal(err)
	}
	return sp
}

// TestAmendSpecSeedsDraftFromRatified (or-tcs.5.1): a refactor on a repo with
// a ratified spec starts a NEW draft version SEEDED from the prior spec —
// requirements and decisions carried in, lineage recorded — so the developer
// EDITS rather than re-elicits from scratch. The ratified parent is untouched.
func TestAmendSpecSeedsDraftFromRatified(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}
	answerFunctional(t, c, ctx)
	if err := c.AddRequirement(ctx, spec.Requirement{
		Source: completeness.DimFunctional,
		Text:   "GET /time returns an RFC3339 time key",
		Cases: []spec.BehavioralCase{{
			Request: spec.RequestShape{Method: "GET", Path: "/time"},
			Expect: spec.ExpectShape{Status: 200, ContentType: "application/json",
				Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyRFC3339, Key: "time"}}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatal(err)
	}
	parent := latestSpecRow(t, c, ctx)
	if parent.Status != "accepted" {
		t.Fatalf("precondition: ratified spec, got %s", parent.Status)
	}

	av, err := c.AmendSpec(ctx)
	if err != nil {
		t.Fatalf("amend: %v", err)
	}
	draft := latestSpecRow(t, c, ctx)
	if draft.Status != "drafting" || draft.Version != parent.Version+1 || draft.ParentSpecID != parent.ID {
		t.Fatalf("amendment must be a new draft version with lineage, got status=%s v=%d parent=%q", draft.Status, draft.Version, draft.ParentSpecID)
	}
	if draft.Requirements != parent.Requirements {
		t.Fatal("the draft must be SEEDED with the parent's requirements")
	}
	if av.RequirementsCarried == 0 || av.DecisionsCarried == 0 {
		t.Fatalf("the amend view must report what was carried, got %+v", av)
	}
	// Decisions were copied onto the new spec id.
	var n int
	_ = c.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		ds, e := tx.Decisions().ListForSpec(ctx, draft.ID)
		n = len(ds)
		return e
	})
	if n != av.DecisionsCarried {
		t.Fatalf("carried decisions must live on the draft: %d vs %d", n, av.DecisionsCarried)
	}
	// The ratified parent row is untouched.
	var again contextstore.Spec
	_ = c.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		s, e := tx.Specs().Get(ctx, parent.ID)
		again = s
		return e
	})
	if again.Status != "accepted" || again.Hash != parent.Hash || again.Requirements != parent.Requirements {
		t.Fatal("amending must never mutate the ratified parent")
	}
}

// TestAmendSpecRefusesWhileDrafting: amendment starts from a RATIFIED spec —
// mid-draft there is nothing to amend, keep editing the draft.
func TestAmendSpecRefusesWhileDrafting(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AmendSpec(ctx); err == nil || !strings.Contains(err.Error(), "draft") {
		t.Fatalf("amending a draft must refuse with guidance, got %v", err)
	}
}

// TestAmendedDraftEditsIndependentlyAndReratifies: edits land on the draft
// (never the parent), and re-ratification composes with the or-7et.2 guard —
// the old plan reads stale against the NEW anchor.
func TestAmendedDraftEditsIndependentlyAndReratifies(t *testing.T) {
	c, ctx := storeConductor(t)
	driveToPlan(t, c, ctx)
	if _, err := c.PlanView(ctx); err != nil { // materialize the plan on the v1 anchor
		t.Fatal(err)
	}
	parent := latestSpecRow(t, c, ctx)

	if _, err := c.AmendSpec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.AddRequirement(ctx, spec.Requirement{
		Source: completeness.DimFunctional,
		Text:   "GET /version reports the build",
		Cases: []spec.BehavioralCase{{
			Request: spec.RequestShape{Method: "GET", Path: "/version"},
			Expect: spec.ExpectShape{Status: 200, ContentType: "application/json",
				Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyPresent, Key: "version"}}},
		}},
	}); err != nil {
		t.Fatalf("edit the draft: %v", err)
	}
	var parentAgain contextstore.Spec
	_ = c.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		s, e := tx.Specs().Get(ctx, parent.ID)
		parentAgain = s
		return e
	})
	if parentAgain.Requirements != parent.Requirements {
		t.Fatal("editing the amendment draft must not touch the parent's requirements")
	}

	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatalf("re-ratify the amendment: %v", err)
	}
	if _, err := c.PlanView(ctx); !errors.Is(err, ErrPlanStale) {
		t.Fatalf("the v1 plan must read STALE against the amended anchor (or-7et.2), got %v", err)
	}
}
