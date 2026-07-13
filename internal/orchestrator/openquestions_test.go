package orchestrator

import (
	"strings"
	"testing"
)

// TestOpenQuestionLifecycle (or-045a.6 DONE-WHEN a+b): a deferred ambiguity is
// RAISED into the ledger (it no longer vanishes when the grill stops
// re-asking), blocks ratify_goals and ApproveSpec by name, and clears only
// through an explicit answer.
func TestOpenQuestionLifecycle(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, gameIntent); err != nil {
		t.Fatal(err)
	}
	if err := c.SetProjectType(ctx, "game"); err != nil { // clear the or-045a.1 gate first
		t.Fatal(err)
	}
	qid, err := c.RaiseOpenQuestion(ctx, "goals", "grill", "grill.setting", "Is the game mech-based or fantasy-based?", "blocking")
	if err != nil {
		t.Fatal(err)
	}
	qs, err := c.OpenQuestions(ctx)
	if err != nil || len(qs) != 1 || qs[0].Question != "Is the game mech-based or fantasy-based?" {
		t.Fatalf("the raised question must be listed: %+v err=%v", qs, err)
	}

	// ratify_goals refuses while a blocking goals-phase question is open.
	if err := c.ProposeGoals(ctx, GoalsDoc{Goals: []string{"uncanny mech movement"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RatifyGoals(ctx); err == nil || !strings.Contains(err.Error(), "mech-based or fantasy-based") {
		t.Fatalf("ratify_goals must refuse naming the open question, got: %v", err)
	}
	// ApproveSpec refuses too (any phase).
	if _, err := c.ApproveSpec(ctx); err == nil || !strings.Contains(err.Error(), "open question") {
		t.Fatalf("ApproveSpec must refuse while a blocking question is open, got: %v", err)
	}

	// Answered with a key → the answer is RECORDED (not just marked) and the
	// gates clear.
	if err := c.ResolveOpenQuestion(ctx, qid, "answered", "mech-based"); err != nil {
		t.Fatal(err)
	}
	_, sp, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ds, err := c.Store().DecisionsForSpec(ctx, sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	recorded := false
	for _, d := range ds {
		if d.Key == "grill.setting" && d.Value == "mech-based" {
			recorded = true
		}
	}
	if !recorded {
		t.Fatal("an 'answered' resolution with a key must record the answer in the decision lineage")
	}
	if _, err := c.RatifyGoals(ctx); err != nil {
		t.Fatalf("ratify_goals must succeed once the question is answered: %v", err)
	}
	if qs, _ := c.OpenQuestions(ctx); len(qs) != 0 {
		t.Fatalf("an answered question must leave the open ledger: %+v", qs)
	}
}

// TestAssumedResolutionFlowsToExistingAudit (or-045a.6 DONE-WHEN c): resolving
// a question as ASSUMED records an approved assumption in the SAME ledger the
// or-v9f.19 gate audits — no second approval machinery.
func TestAssumedResolutionFlowsToExistingAudit(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, gameIntent); err != nil {
		t.Fatal(err)
	}
	qid, err := c.RaiseOpenQuestion(ctx, "direction", "grill", "direction.engine", "Which engine renders the client?", "blocking")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ResolveOpenQuestion(ctx, qid, "assumed", "none"); err != nil {
		t.Fatal(err)
	}
	_, sp, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ds, err := c.Store().DecisionsForSpec(ctx, sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range ds {
		if d.Key == "direction.engine" && d.Value == "none" && d.ValueKind == "assumption_approved" {
			found = true
		}
	}
	if !found {
		t.Fatalf("an assumed resolution must land as assumption_approved in the existing audit: %+v", ds)
	}
	if qs, _ := c.OpenQuestions(ctx); len(qs) != 0 {
		t.Fatalf("an assumed question must leave the open ledger: %+v", qs)
	}
}

// Guardrails (or-045a.6 DONE-WHEN d — no silent path): only answered|assumed
// resolve a question, an unknown id is refused, and ADVISORY questions never
// block ratification.
func TestOpenQuestionGuardrails(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, largeHTTPIntent); err != nil {
		t.Fatal(err)
	}
	qid, err := c.RaiseOpenQuestion(ctx, "spec", "developer", "", "Should retries be exponential?", "advisory")
	if err != nil {
		t.Fatal(err)
	}
	// Invalid resolution kinds and unknown ids are refused.
	if err := c.ResolveOpenQuestion(ctx, qid, "ignored", "x"); err == nil {
		t.Fatal("only answered|assumed may resolve a question")
	}
	if err := c.ResolveOpenQuestion(ctx, "no-such-id", "answered", "x"); err == nil {
		t.Fatal("an unknown question id must be refused")
	}
	// An advisory question does NOT block the spec (the large-scale flow still
	// hits its OTHER gates — assert the failure is not the open-question gate).
	answerFunctional(t, c, ctx)
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApproveSpec(ctx); err == nil || strings.Contains(err.Error(), "open question") {
		t.Fatalf("an advisory question must not be the blocker, got: %v", err)
	}
	// A raise with an invalid severity/phase is refused (closed vocabulary).
	if _, err := c.RaiseOpenQuestion(ctx, "bogus-phase", "grill", "", "q", "blocking"); err == nil {
		t.Fatal("an unknown phase must be refused")
	}
	if _, err := c.RaiseOpenQuestion(ctx, "spec", "grill", "", "q", "sometimes"); err == nil {
		t.Fatal("an unknown severity must be refused")
	}
}
