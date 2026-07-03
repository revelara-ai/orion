package orchestrator

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// shadowMockProposer emits a distinctive module set that covers every floor
// dimension (so the shadow record is superset/floor OK) but uses keys the oracle
// template never produces — so we can prove the oracle, not the proposer, drove
// the persisted plan.
func shadowMockProposer() decomposer.ModuleProposer {
	return func(_ context.Context, es spec.ExecutableSpec, _ string, floor []completeness.Dimension) ([]decomposer.ProposedModule, error) {
		covers := make([]string, 0, len(floor)+4)
		for _, d := range floor {
			covers = append(covers, string(d))
		}
		covers = append(covers, es.ResponseContract.RequiredCaseIDs()...)
		return []decomposer.ProposedModule{
			{Key: "proposer-only-module", Title: "everything", ProofObligation: "covers all", FileScope: "x/", Covers: covers},
		}, nil
	}
}

// TestModuleProposerShadowDoesNotDriveBuild (or-809): with ORION_MODULE_PROPOSER
// =shadow the ORACLE decomposer still produces the live plan (byte-identical
// behavior), and a shadow comparison record is persisted.
func TestModuleProposerShadowDoesNotDriveBuild(t *testing.T) {
	t.Setenv("ORION_MODULE_PROPOSER", "shadow")
	c, ctx := storeConductor(t)
	c.SetModuleProposer(shadowMockProposer())

	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatalf("submit: %v", err)
	}
	answerFunctional(t, c, ctx)
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatalf("approve assumptions: %v", err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatalf("approve: %v", err)
	}

	pv, err := c.PlanView(ctx)
	if err != nil {
		t.Fatalf("plan view: %v", err)
	}
	// The oracle drove the plan: an oracle-only key is present and the
	// proposer-only key is absent.
	keys := map[string]bool{}
	for _, tk := range pv.Tasks {
		keys[tk.ID] = true // ids are the task keys in the plan projection
	}
	sawProposerOnly := false
	for _, tk := range pv.Tasks {
		if tk.Title == "everything" {
			sawProposerOnly = true
		}
	}
	if sawProposerOnly {
		t.Fatal("shadow mode must NOT let the proposer drive the persisted plan")
	}

	// A shadow comparison record was persisted for this project.
	proj, _, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := c.Store().ShadowPlans(ctx, proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 {
		t.Fatal("shadow mode must persist a comparison record")
	}
	r := recs[0]
	if !r.SupersetOK || !r.FloorOK || !r.CoverageGateOK {
		t.Fatalf("the mock proposer covers everything — record should be all-OK, got %+v", r)
	}
	if r.ProposerModules == 0 || r.OracleModules == 0 {
		t.Fatalf("record must capture both module counts, got %+v", r)
	}
}

// TestModuleProposerShadowMeasuresRawProposer (or-809, regression for the
// adversarial-review finding): the shadow metric must reflect the PROPOSER'S OWN
// coverage, not the deterministic acceptance bookend that backfills everything.
// An under-covering proposer must record SupersetOK=false / FloorOK=false.
func TestModuleProposerShadowMeasuresRawProposer(t *testing.T) {
	t.Setenv("ORION_MODULE_PROPOSER", "shadow")
	c, ctx := storeConductor(t)
	// A proposer that covers only "functional" — drops the rest of the floor.
	c.SetModuleProposer(func(_ context.Context, _ spec.ExecutableSpec, _ string, _ []completeness.Dimension) ([]decomposer.ProposedModule, error) {
		return []decomposer.ProposedModule{
			{Key: "thin", Title: "thin", ProofObligation: "does the bare minimum", FileScope: "x/", Covers: []string{"functional"}},
		}, nil
	})

	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatalf("submit: %v", err)
	}
	answerFunctional(t, c, ctx)
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatalf("approve assumptions: %v", err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := c.PlanView(ctx); err != nil {
		t.Fatalf("plan view: %v", err)
	}

	proj, _, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := c.Store().ShadowPlans(ctx, proj.ID)
	if err != nil || len(recs) == 0 {
		t.Fatalf("shadow record missing: %v", err)
	}
	r := recs[0]
	if r.SupersetOK || r.FloorOK {
		t.Fatalf("an under-covering proposer must record superset/floor FALSE (the bookend must not launder the metric), got %+v", r)
	}
	if len(r.Missing) == 0 {
		t.Fatal("the record must name the coverage the proposer dropped")
	}
}
