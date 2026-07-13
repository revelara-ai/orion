package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

func grillReturning(qs []completeness.OpenDecision, err error) (GrillAgent, *int) {
	calls := new(int)
	return func(context.Context, string, map[string]string, []completeness.OpenDecision) ([]completeness.OpenDecision, error) {
		*calls++
		return qs, err
	}, calls
}

// TestGrillDrivesElicitation (or-794, V3 Step 5): with ORION_ELICITATION=grill
// the grill's open-ended questions LEAD and every unresolved floor dimension
// still follows — demoted, never dropped.
func TestGrillDrivesElicitation(t *testing.T) {
	t.Setenv("ORION_ELICITATION", "grill")
	c, ctx := storeConductor(t)
	grill, _ := grillReturning([]completeness.OpenDecision{
		{Key: "grill.success", Question: "What does success look like a month after launch?"},
		{Key: "grill.out_of_scope", Question: "What is explicitly OUT of scope?"},
	}, nil)
	c.SetGrillAgent(grill)

	conf, err := c.Submit(ctx, flowIntent)
	if err != nil {
		t.Fatal(err)
	}
	if len(conf.OpenDecisions) < 3 {
		t.Fatalf("grill questions + unresolved floor must both surface, got %d", len(conf.OpenDecisions))
	}
	if conf.OpenDecisions[0].Key != "grill.success" || conf.OpenDecisions[1].Key != "grill.out_of_scope" {
		t.Fatalf("the grill's questions must LEAD, got %+v", conf.OpenDecisions[:2])
	}
	var floorSeen bool
	for _, d := range conf.OpenDecisions {
		if d.Key == "response_format" {
			floorSeen = true
		}
	}
	if !floorSeen {
		t.Fatal("the demoted floor must still surface its unresolved dimensions")
	}
}

// TestGrillAnswersAnchorInSpec: a grill answer records like any decision and
// rides the compiled spec (and its hash) — anchored intent-altitude content.
func TestGrillAnswersAnchorInSpec(t *testing.T) {
	t.Setenv("ORION_ELICITATION", "grill")
	c, ctx := storeConductor(t)
	grill, _ := grillReturning([]completeness.OpenDecision{
		{Key: "grill.success", Question: "What does success look like?"},
	}, nil)
	c.SetGrillAgent(grill)

	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}
	if err := c.RecordAnswer(ctx, "grill.success", "the team stops asking for the time in slack"); err != nil {
		t.Fatal(err)
	}
	answerFunctional(t, c, ctx) // resolve the floor
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatal(err)
	}
	es, err := c.RecallSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(es.Decisions["grill.success"], "stops asking") {
		t.Fatalf("a grill answer must anchor in the ratified spec: %+v", es.Decisions)
	}
}

// TestGrillFailsOpenToChecklist: a grill error (or panic) reverts to the
// checklist driver — elicitation never breaks on the LLM.
func TestGrillFailsOpenToChecklist(t *testing.T) {
	t.Setenv("ORION_ELICITATION", "grill")
	c, ctx := storeConductor(t)
	// Partial results + an error: fail-open must discard the partial
	// questions, not surface half a grill.
	grill, calls := grillReturning([]completeness.OpenDecision{{Key: "grill.broken", Question: "?"}}, errors.New("brain offline"))
	c.SetGrillAgent(grill)

	conf, err := c.Submit(ctx, flowIntent)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("the grill must have been consulted, calls=%d", *calls)
	}
	if len(conf.OpenDecisions) == 0 || strings.HasPrefix(conf.OpenDecisions[0].Key, "grill.") {
		t.Fatalf("a grill failure must fail open to the checklist floor: %+v", conf.OpenDecisions)
	}

	// Panic path: same fail-open.
	c2, ctx2 := storeConductor(t)
	c2.SetGrillAgent(func(context.Context, string, map[string]string, []completeness.OpenDecision) ([]completeness.OpenDecision, error) {
		panic("grill exploded")
	})
	conf2, err := c2.Submit(ctx2, flowIntent)
	if err != nil {
		t.Fatal(err)
	}
	if len(conf2.OpenDecisions) == 0 {
		t.Fatal("a grill panic must fail open to the checklist floor")
	}
}

// TestElicitationDefaultUnchanged: without ORION_ELICITATION=grill the
// checklist drives byte-identically and the grill is NEVER invoked — the
// reversibility contract of the riskiest-last step.
func TestElicitationDefaultUnchanged(t *testing.T) {
	t.Setenv("ORION_ELICITATION", "")
	c, ctx := storeConductor(t)
	grill, calls := grillReturning([]completeness.OpenDecision{{Key: "grill.x", Question: "?"}}, nil)
	c.SetGrillAgent(grill)

	conf, err := c.Submit(ctx, flowIntent)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 0 {
		t.Fatal("default mode must never invoke the grill")
	}
	for _, d := range conf.OpenDecisions {
		if strings.HasPrefix(d.Key, "grill.") {
			t.Fatalf("default mode must be the checklist alone: %+v", conf.OpenDecisions)
		}
	}
}
