package orchestrator

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/proof/formal"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
)

const designDraft = `# candidate design model
# obligation: NoDoubleDispatch -> go test ./internal/x -run TestNoDoubleDispatch

action Init:
    state = "idle"

always assertion NoDoubleDispatch:
    state != "double"
`

func ratifiedSTPA(t *testing.T, ctx context.Context, c *Conductor, controllers []string) string {
	t.Helper()
	proj, _, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatalf("current project: %v", err)
	}
	m := stpa.Model{
		Structure: stpa.ControlStructure{Controllers: controllers},
		UCAs:      []stpa.UCA{{ID: "UCA1", ControlAction: "CA1", Type: stpa.ProvidedIncorrectly, Hazard: "double dispatch", Disposition: stpa.DispositionControlled}},
	}
	if err := stpa.Save(ctx, c.Store(), proj.ID, m); err != nil {
		t.Fatalf("save stpa: %v", err)
	}
	return proj.ID
}

// TestPlanDraftsDesignModelForConcurrentStructure (or-56c.2): the design-time
// placement — after spec+STPA, the plan path drafts a ratifiable model when
// the ratified control structure is coordination by construction (≥2
// controllers). The plan itself is never blocked by the draft.
func TestPlanDraftsDesignModelForConcurrentStructure(t *testing.T) {
	c, ctx := storeConductor(t)
	c.SetModelSynthesizer(func(context.Context, formal.SynthesisInput) (string, error) { return designDraft, nil })

	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}
	answerFunctional(t, c, ctx)
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatal(err)
	}
	pid := ratifiedSTPA(t, ctx, c, []string{"scheduler", "worker"})
	if _, err := c.PlanView(ctx); err != nil {
		t.Fatalf("a design-model draft must never block the plan: %v", err)
	}
	dm, ok, err := formal.LoadDesignModel(ctx, c.Store(), pid)
	if err != nil || !ok {
		t.Fatalf("the plan path must persist a design-model draft: ok=%v err=%v", ok, err)
	}
	if dm.Ratified || dm.Hash == "" || dm.Backend != "fizzbee" {
		t.Fatalf("the draft must be unratified, hash-anchored, backend-recorded: %+v", dm)
	}
}

// TestPlanDraftsNothingForStatelessStructure (or-56c.2): a single-controller,
// shape-free spec is calibrated off — the plan produces no artifact.
func TestPlanDraftsNothingForStatelessStructure(t *testing.T) {
	c, ctx := storeConductor(t)
	c.SetModelSynthesizer(func(context.Context, formal.SynthesisInput) (string, error) { return designDraft, nil })

	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}
	answerFunctional(t, c, ctx)
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatal(err)
	}
	pid := ratifiedSTPA(t, ctx, c, []string{"api"})
	if _, err := c.PlanView(ctx); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, ok, _ := formal.LoadDesignModel(ctx, c.Store(), pid); ok {
		t.Fatal("a stateless spec must produce no design-model artifact")
	}
}
