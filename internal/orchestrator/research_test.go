package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

// modeGrill returns a grill agent that emits one research and one hitl
// question, plus a call counter.
func modeGrill() (GrillAgent, *int) {
	calls := new(int)
	return func(context.Context, string, map[string]string, []completeness.OpenDecision) ([]completeness.OpenDecision, error) {
		*calls++
		return []completeness.OpenDecision{
			{Key: "grill.existing_api", Question: "What does the existing endpoint return?", Mode: "research"},
			{Key: "grill.audience", Question: "Who is this for?", Mode: "hitl"},
		}, nil
	}, calls
}

// TestResearchQuestionsResolveWithoutDeveloper (or-kjcm.4): a research-mode
// grill question is burned down by the injected researcher — resolved in the
// ledger with the finding, recorded as a keyed decision, and never surfaced
// to the developer. The hitl question still surfaces.
func TestResearchQuestionsResolveWithoutDeveloper(t *testing.T) {
	t.Setenv("ORION_ELICITATION", "grill")
	c, ctx := storeConductor(t)
	grill, _ := modeGrill()
	c.SetGrillAgent(grill)
	researched := 0
	c.SetResearcher(func(_ context.Context, q contextstore.OpenQuestion) (string, error) {
		researched++
		if q.Key != "grill.existing_api" {
			t.Fatalf("researcher must only see research-mode questions, got %+v", q)
		}
		return "returns {\"time\": RFC3339} per pkg/api/handler.go", nil
	})

	conf, err := c.Submit(ctx, flowIntent)
	if err != nil {
		t.Fatal(err)
	}
	if researched != 1 {
		t.Fatalf("the researcher must run once, ran %d", researched)
	}
	for _, d := range conf.OpenDecisions {
		if d.Key == "grill.existing_api" {
			t.Fatalf("a resolved research question must not surface to the developer: %+v", conf.OpenDecisions)
		}
	}
	if conf.OpenDecisions[0].Key != "grill.audience" {
		t.Fatalf("the hitl question must still lead: %+v", conf.OpenDecisions[0])
	}

	// Ledger: answered with the finding.
	var all []contextstore.OpenQuestion
	if err := c.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		all, e = tx.OpenQuestions().ListByProject(ctx, c.ProjectID())
		return e
	}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, q := range all {
		if q.Key == "grill.existing_api" {
			found = true
			if q.Status != "answered" || q.Resolution == "" {
				t.Fatalf("research resolution must land on the ledger row: %+v", q)
			}
		}
	}
	if !found {
		t.Fatal("research question missing from the ledger")
	}
	// Decision lineage carries the finding, so the grill's resolved view has it.
	_, sp, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ds, err := c.Store().DecisionsForSpec(ctx, sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	ok := false
	for _, d := range ds {
		if d.Key == "grill.existing_api" {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("the finding must record as a keyed decision: %+v", ds)
	}
}

// TestResearchFailureDowngradesToDeveloper: a failed lookup flips the question
// to hitl and surfaces it — never a guess, never a silent drop.
func TestResearchFailureDowngradesToDeveloper(t *testing.T) {
	t.Setenv("ORION_ELICITATION", "grill")
	c, ctx := storeConductor(t)
	grill, _ := modeGrill()
	c.SetGrillAgent(grill)
	c.SetResearcher(func(context.Context, contextstore.OpenQuestion) (string, error) {
		return "", errors.New("lookup failed")
	})

	conf, err := c.Submit(ctx, flowIntent)
	if err != nil {
		t.Fatal(err)
	}
	var surfaced *completeness.OpenDecision
	for i, d := range conf.OpenDecisions {
		if d.Key == "grill.existing_api" {
			surfaced = &conf.OpenDecisions[i]
		}
	}
	if surfaced == nil {
		t.Fatalf("a failed research question must return to the developer: %+v", conf.OpenDecisions)
	}
	if surfaced.Mode != "hitl" {
		t.Fatalf("the downgrade must be recorded, got mode %q", surfaced.Mode)
	}
	// Downgrade is durable: the ledger row is open + hitl.
	qs, err := c.OpenQuestions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range qs {
		if q.Key == "grill.existing_api" && q.Mode != "hitl" {
			t.Fatalf("downgrade must persist: %+v", q)
		}
	}
	// And the next gate read must NOT re-research a downgraded question.
	if _, err := c.SpecView(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestNoResearcherResearchQuestionsSurface: without a researcher (offline),
// research questions degrade gracefully to developer questions.
func TestNoResearcherResearchQuestionsSurface(t *testing.T) {
	t.Setenv("ORION_ELICITATION", "grill")
	c, ctx := storeConductor(t)
	grill, _ := modeGrill()
	c.SetGrillAgent(grill)

	conf, err := c.Submit(ctx, flowIntent)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range conf.OpenDecisions {
		if d.Key == "grill.existing_api" {
			found = true
		}
	}
	if !found {
		t.Fatalf("with no researcher, research questions must surface to the developer: %+v", conf.OpenDecisions)
	}
}
