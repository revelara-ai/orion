package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

func planTitles(t *testing.T, c *Conductor, ctx context.Context) map[string]bool {
	t.Helper()
	pv, err := c.PlanView(ctx)
	if err != nil {
		t.Fatalf("plan view: %v", err)
	}
	titles := map[string]bool{}
	for _, tk := range pv.Tasks {
		titles[tk.Title] = true
	}
	return titles
}

func driveToPlan(t *testing.T, c *Conductor, ctx context.Context) {
	t.Helper()
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
}

// TestModuleProposerLiveDrivesPlanWhenGatesHold (or-809 cutover half): with
// ORION_MODULE_PROPOSER=live and a proposal that passes the deterministic
// trust wall (ReconcileFloor + CoverageGate + oracle-coverage superset), the
// PROPOSER's plan drives the build — including Orion's synthesized
// whole-intent bookend, never the proposer's own.
func TestModuleProposerLiveDrivesPlanWhenGatesHold(t *testing.T) {
	t.Setenv("ORION_MODULE_PROPOSER", "live")
	c, ctx := storeConductor(t)
	c.SetModuleProposer(shadowMockProposer()) // covers every floor dim + case id

	driveToPlan(t, c, ctx)

	titles := planTitles(t, c, ctx)
	if !titles["everything"] {
		t.Fatalf("live mode must let a gate-passing proposer drive the plan; got %v", titles)
	}
	if !titles["Whole-intent acceptance"] {
		t.Fatalf("the live plan must carry Orion's synthesized bookend; got %v", titles)
	}
}

// TestModuleProposerLiveFallsBackToOracleOnGateFailure: a proposal that fails
// the deterministic wall (misses coverage) must NOT drive the plan and must
// NOT fail it either — the oracle drives, exactly as in shadow mode.
func TestModuleProposerLiveFallsBackToOracleOnGateFailure(t *testing.T) {
	t.Setenv("ORION_MODULE_PROPOSER", "live")
	c, ctx := storeConductor(t)
	c.SetModuleProposer(func(_ context.Context, _ spec.ExecutableSpec, _ string, _ []completeness.Dimension) ([]decomposer.ProposedModule, error) {
		return []decomposer.ProposedModule{
			{Key: "narrow", Title: "narrow slice", ProofObligation: "p", Covers: []string{"nothing-real"}},
		}, nil
	})

	driveToPlan(t, c, ctx)

	titles := planTitles(t, c, ctx)
	if titles["narrow slice"] {
		t.Fatal("a gate-failing proposal must never drive the plan")
	}
	if len(titles) == 0 {
		t.Fatal("fallback must still produce the oracle plan")
	}
}

// TestModuleProposerLiveCoverageGateIndependentlyBlocks: a proposal that
// PASSES the floor (all reliability dims covered) but drops a required case id
// must still fall back — each wall layer blocks independently.
func TestModuleProposerLiveCoverageGateIndependentlyBlocks(t *testing.T) {
	t.Setenv("ORION_MODULE_PROPOSER", "live")
	c, ctx := storeConductor(t)
	c.SetModuleProposer(func(_ context.Context, es spec.ExecutableSpec, _ string, floor []completeness.Dimension) ([]decomposer.ProposedModule, error) {
		covers := make([]string, 0, len(floor))
		for _, d := range floor {
			covers = append(covers, string(d)) // floor fully covered…
		}
		// …but NO required case ids: CoverageGate must reject.
		return []decomposer.ProposedModule{
			{Key: "floor-only", Title: "floor only", ProofObligation: "p", Covers: covers},
		}, nil
	})

	driveToPlan(t, c, ctx)

	if planTitles(t, c, ctx)["floor only"] {
		t.Fatal("a coverage-gate failure must fall back to the oracle even when the floor holds")
	}
}

// TestModuleProposerLiveFallsBackOnProposerError: a proposer ERROR degrades to
// the oracle, never fails the plan.
func TestModuleProposerLiveFallsBackOnProposerError(t *testing.T) {
	t.Setenv("ORION_MODULE_PROPOSER", "live")
	c, ctx := storeConductor(t)
	c.SetModuleProposer(func(_ context.Context, _ spec.ExecutableSpec, _ string, _ []completeness.Dimension) ([]decomposer.ProposedModule, error) {
		return nil, errors.New("model unavailable")
	})

	driveToPlan(t, c, ctx)

	if len(planTitles(t, c, ctx)) == 0 {
		t.Fatal("a proposer error must fall back to the oracle plan")
	}
}
