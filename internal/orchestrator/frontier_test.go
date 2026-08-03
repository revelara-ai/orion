package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

// edgeGrill emits four questions with the scope question wired to block the
// two detail questions — deliberately listing the details FIRST, so any test
// that passes proves presentation order comes from the edges, not from the
// LLM's output order.
func edgeGrill() (GrillAgent, *int) {
	calls := new(int)
	return func(context.Context, string, map[string]string, []completeness.OpenDecision) ([]completeness.OpenDecision, error) {
		*calls++
		return []completeness.OpenDecision{
			{Key: "grill.retention", Question: "How long is history kept?", BlockedBy: []string{"grill.scope"}},
			{Key: "grill.export", Question: "Which export formats?", BlockedBy: []string{"grill.scope"}},
			{Key: "grill.scope", Question: "Is this for one team or the whole org?"},
			{Key: "grill.audience", Question: "Who reads the output?"},
		}, nil
	}, calls
}

// TestFrontierHoldsBlockedQuestions (or-kjcm.7): dependency-blocked questions
// stay off the developer surface until the blocking answer lands; the served
// order is independent of the grill's emission order.
func TestFrontierHoldsBlockedQuestions(t *testing.T) {
	t.Setenv("ORION_ELICITATION", "grill")
	c, ctx := storeConductor(t)
	grill, _ := edgeGrill()
	c.SetGrillAgent(grill)

	conf, err := c.Submit(ctx, flowIntent)
	if err != nil {
		t.Fatal(err)
	}
	served := map[string]bool{}
	var grillKeys []string
	for _, d := range conf.OpenDecisions {
		served[d.Key] = true
		if strings.HasPrefix(d.Key, "grill.") {
			grillKeys = append(grillKeys, d.Key)
		}
	}
	if served["grill.retention"] || served["grill.export"] {
		t.Fatalf("detail questions blocked on scope must be held back: %v", grillKeys)
	}
	if !served["grill.scope"] || !served["grill.audience"] {
		t.Fatalf("unblocked questions must surface: %v", grillKeys)
	}
	// The scope question leads even though the grill emitted it third.
	if grillKeys[0] != "grill.scope" {
		t.Fatalf("the scope-fixing question must come first regardless of emission order: %v", grillKeys)
	}

	// Answering the blocker opens the frontier — the details surface now.
	if err := c.RecordAnswer(ctx, "grill.scope", "one team"); err != nil {
		t.Fatal(err)
	}
	v, err := c.SpecView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after := map[string]bool{}
	for _, d := range v.OpenDecisions {
		after[d.Key] = true
	}
	if !after["grill.retention"] || !after["grill.export"] {
		t.Fatalf("resolving the blocker must surface the held questions: %+v", v.OpenDecisions)
	}
	if after["grill.scope"] {
		t.Fatalf("the answered blocker must not resurface: %+v", v.OpenDecisions)
	}
}

// TestFrontierDanglingEdgeFailsOpen: an edge naming a key that is neither an
// open question nor answered is ignored — a dangling edge never strands a
// question off the surface.
func TestFrontierDanglingEdgeFailsOpen(t *testing.T) {
	t.Setenv("ORION_ELICITATION", "grill")
	c, ctx := storeConductor(t)
	grill, _ := grillReturning([]completeness.OpenDecision{
		{Key: "grill.export", Question: "Which export formats?", BlockedBy: []string{"grill.never_asked"}},
	}, nil)
	c.SetGrillAgent(grill)
	conf, err := c.Submit(ctx, flowIntent)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range conf.OpenDecisions {
		if d.Key == "grill.export" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a dangling edge must not strand the question: %+v", conf.OpenDecisions)
	}
}
