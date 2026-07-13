package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

func timeReq() spec.Requirement {
	return spec.Requirement{
		Source: completeness.DimFunctional,
		Text:   "GET /time returns an RFC3339 time key",
		Cases: []spec.BehavioralCase{{
			Request: spec.RequestShape{Method: "GET", Path: "/time"},
			Expect: spec.ExpectShape{Status: 200, ContentType: "application/json",
				Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyRFC3339, Key: "time"}}},
		}},
	}
}

func versionReq() spec.Requirement {
	return spec.Requirement{
		Source: completeness.DimFunctional,
		Text:   "GET /version reports the build",
		Cases: []spec.BehavioralCase{{
			Request: spec.RequestShape{Method: "GET", Path: "/version"},
			Expect: spec.ExpectShape{Status: 200, ContentType: "application/json",
				Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyPresent, Key: "version"}}},
		}},
	}
}

// ratifyWithReq drives submit→answers→AddRequirement(reqs)→approve.
func ratifyWithReq(t *testing.T, c *Conductor, ctx context.Context, reqs ...spec.Requirement) {
	t.Helper()
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}
	answerFunctional(t, c, ctx)
	for _, r := range reqs {
		if err := c.AddRequirement(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestReconcileInvalidatesOnlyCoveringTasks (or-7et.2 slice 2 Done-when core):
// amending ONE of N requirements re-plans with only the tasks covering the
// changed surface (plus dependents) marked for re-proof; untouched tasks keep
// memo reuse eligibility, and the plan reads fresh again (no ErrPlanStale).
func TestReconcileInvalidatesOnlyCoveringTasks(t *testing.T) {
	c, ctx := storeConductor(t)
	ratifyWithReq(t, c, ctx, timeReq(), versionReq())
	if _, err := c.PlanView(ctx); err != nil { // materialize the v1 plan
		t.Fatal(err)
	}

	// Amend: change ONE requirement (the /version one gains an assertion).
	if _, err := c.AmendSpec(ctx); err != nil {
		t.Fatal(err)
	}
	// IDs are content-addressed: a CHANGE is remove(old)+add(new).
	old := versionReq()
	old.SetIDs()
	if err := c.RemoveRequirement(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	vr := versionReq()
	vr.Cases[0].Expect.Assertions = append(vr.Cases[0].Expect.Assertions,
		spec.BodyAssertion{Kind: spec.AssertJSONKeyPresent, Key: "commit"})
	if err := c.AddRequirement(ctx, vr); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatal(err)
	}

	pv, err := c.PlanView(ctx)
	if err != nil {
		t.Fatalf("after reconciliation the plan must read FRESH, got %v", err)
	}
	var invalidated, kept []string
	for _, tk := range pv.Tasks {
		if tk.ReproofRequired {
			invalidated = append(invalidated, tk.Title)
		} else {
			kept = append(kept, tk.Title)
		}
	}
	if len(invalidated) == 0 {
		t.Fatalf("the changed requirement's covering task(s) must be invalidated; plan: %+v", pv.Tasks)
	}
	if len(kept) == 0 {
		t.Fatalf("amending ONE requirement must not invalidate the whole plan (kept=0, invalidated=%v)", invalidated)
	}
	// Dimension-scoped tasks (observability/scale/etc.) are untouched by a case
	// change and must be kept; the case-covering handler must not be.
	keptJoined := strings.Join(kept, " | ")
	if !strings.Contains(keptJoined, "structured logs") || !strings.Contains(keptJoined, "capacity") {
		t.Fatalf("dimension tasks untouched by the case change must be kept, kept=%v invalidated=%v", kept, invalidated)
	}
	if !strings.Contains(strings.Join(invalidated, " | "), "handler") {
		t.Fatalf("the case-covering handler task must be invalidated, invalidated=%v", invalidated)
	}
}

// TestReconcileRecordsRealignmentEvent (slice 3): the reconciliation surfaces a
// named realignment record listing the diff and the kept/invalidated split.
func TestReconcileRecordsRealignmentEvent(t *testing.T) {
	c, ctx := storeConductor(t)
	ratifyWithReq(t, c, ctx, timeReq(), versionReq())
	if _, err := c.PlanView(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AmendSpec(ctx); err != nil {
		t.Fatal(err)
	}
	old := versionReq()
	old.SetIDs()
	if err := c.RemoveRequirement(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	vr := versionReq()
	vr.Text = "GET /version reports the build AND commit"
	vr.Cases[0].Expect.Assertions = append(vr.Cases[0].Expect.Assertions,
		spec.BodyAssertion{Kind: spec.AssertJSONKeyPresent, Key: "commit"})
	if err := c.AddRequirement(ctx, vr); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatal(err)
	}

	proj, _, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found string
	_ = c.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		open, e := tx.Escalations().ListOpen(ctx)
		if e != nil {
			return e
		}
		for _, esc := range open {
			if esc.ProjectID == proj.ID && strings.Contains(esc.Reason, "realignment") {
				found = esc.Detail
			}
		}
		return nil
	})
	if found == "" {
		t.Fatal("reconciliation must record a named realignment event")
	}
	for _, want := range []string{"invalidated", "kept"} {
		if !strings.Contains(found, want) {
			t.Fatalf("the realignment record must list the %s set:\n%s", want, found)
		}
	}
}

// TestInPlaceReratificationReconcilesConservatively: a re-ratification WITHOUT
// spec lineage (the same spec row mutated in place) has no old surface to diff
// — the plan is rebuilt with EVERY task marked for re-proof (correctness
// first), and reads fresh.
func TestInPlaceReratificationReconcilesConservatively(t *testing.T) {
	c, ctx := storeConductor(t)
	ratifyWithReq(t, c, ctx, timeReq())
	if _, err := c.PlanView(ctx); err != nil {
		t.Fatal(err)
	}
	// In-place: no AmendSpec — add straight onto the accepted spec + re-approve.
	if err := c.AddRequirement(ctx, versionReq()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatal(err)
	}
	pv, err := c.PlanView(ctx)
	if err != nil {
		t.Fatalf("conservative reconciliation must still leave a fresh plan, got %v", err)
	}
	for _, tk := range pv.Tasks {
		if !tk.ReproofRequired {
			t.Fatalf("in-place re-ratification cannot prove any slice unchanged — every task must re-prove, got kept %q", tk.Title)
		}
	}
}
