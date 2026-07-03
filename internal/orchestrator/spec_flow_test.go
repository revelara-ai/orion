package orchestrator

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

func storeConductor(t *testing.T) (*Conductor, context.Context) {
	t.Helper()
	st, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewWithStore(st), context.Background()
}

const flowIntent = "Build an HTTP service that returns the current time."

func answerFunctional(t *testing.T, c *Conductor, ctx context.Context) {
	t.Helper()
	for _, a := range []struct{ k, v string }{
		{"response_format", "json"}, {"timezone", "UTC"}, {"port", "8080"}, {"route", "/time"},
	} {
		if err := c.RecordAnswer(ctx, a.k, a.v); err != nil {
			t.Fatalf("answer %s: %v", a.k, err)
		}
	}
}

// TestSpecFlowApproveYieldsAcceptedZeroOpen: after answering the blocking
// (functional) decisions, approval applies fallbacks for the rest and the spec
// becomes accepted with zero open decisions (acceptance predicate intent-gate/2).
func TestSpecFlowApproveYieldsAcceptedZeroOpen(t *testing.T) {
	c, ctx := storeConductor(t)
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
	v, err := c.SpecView(ctx)
	if err != nil {
		t.Fatalf("spec view: %v", err)
	}
	if v.Status != "accepted" {
		t.Fatalf("status = %q, want accepted", v.Status)
	}
	if len(v.OpenDecisions) != 0 {
		t.Fatalf("open decisions = %d, want 0", len(v.OpenDecisions))
	}
	if len(v.ResponseContract) == 0 {
		t.Fatal("accepted spec should expose a response contract")
	}
}

// TestApproveRejectsUnansweredBlockingDecision: approval fails if a blocking
// (no-fallback) decision is unanswered — no silent guessing.
func TestApproveRejectsUnansweredBlockingDecision(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// Answer only 3 of the 4 functional decisions.
	for _, a := range []struct{ k, v string }{{"response_format", "json"}, {"timezone", "UTC"}, {"port", "8080"}} {
		if err := c.RecordAnswer(ctx, a.k, a.v); err != nil {
			t.Fatalf("answer: %v", err)
		}
	}
	if _, err := c.ApproveSpec(ctx); err == nil {
		t.Fatal("expected approval to fail with an unanswered blocking decision (route)")
	}
}

// TestRecallSpecAnchorVerified: a recalled accepted spec verifies its anchor; a
// post-approval change to a decision is detected as an anchor mismatch.
func TestRecallSpecAnchorVerified(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatalf("submit: %v", err)
	}
	answerFunctional(t, c, ctx)
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatalf("approve assumptions: %v", err)
	}
	approved, err := c.ApproveSpec(ctx)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}

	recalled, err := c.RecallSpec(ctx)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if recalled.Hash != approved.Hash || !recalled.VerifyAnchor() {
		t.Fatalf("recalled spec anchor mismatch: %q vs %q", recalled.Hash, approved.Hash)
	}

	// Tamper: change a decision after approval; recall must detect the anchor break.
	if err := c.RecordAnswer(ctx, "port", "1"); err != nil {
		t.Fatalf("tamper answer: %v", err)
	}
	if _, err := c.RecallSpec(ctx); err == nil {
		t.Fatal("expected anchor mismatch after a post-approval decision change")
	}
}

// TestRecallLastProvenSpecFallsBackToDelivered: on Accept the project leaves the
// active slot (or-v9f.1), so RecallSpec (active-only) can no longer see it. The
// show_code read path must still resolve the just-proven spec via the delivered
// fallback — otherwise show_code falsely reports "no proven spec" for code it just
// wrote (the state-consistency defect behind internal/conductor
// TestShowCodeReportsLocationAndContent).
func TestRecallLastProvenSpecFallsBackToDelivered(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatalf("submit: %v", err)
	}
	answerFunctional(t, c, ctx)
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatalf("approve assumptions: %v", err)
	}
	accepted, err := c.ApproveSpec(ctx)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}

	// While the spec is active, both resolvers agree on the same anchor.
	if es, rerr := c.RecallLastProvenSpec(ctx); rerr != nil || es.Hash != accepted.Hash {
		t.Fatalf("active: RecallLastProvenSpec = (%q, %v), want %q", es.Hash, rerr, accepted.Hash)
	}

	// Deliver: move the project out of the active slot exactly as the build path does.
	proj, _, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatalf("resolve active project: %v", err)
	}
	if err := c.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.Projects().SetStatus(ctx, proj.ID, "delivered")
	}); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	// Precondition of the bug: the active slot is now empty, so RecallSpec fails.
	if _, err := c.RecallSpec(ctx); err == nil {
		t.Fatal("expected RecallSpec to fail once the project has left the active slot")
	}
	// The fix: show_code's resolver falls back to the delivered spec, anchor intact.
	recalled, err := c.RecallLastProvenSpec(ctx)
	if err != nil {
		t.Fatalf("post-delivery RecallLastProvenSpec: %v", err)
	}
	if recalled.Hash != accepted.Hash || !recalled.VerifyAnchor() {
		t.Fatalf("delivered recall anchor mismatch: %q vs %q", recalled.Hash, accepted.Hash)
	}
}

// TestPlanViewResolvesDeliveredProject: `orion plan show` (PlanView) must still
// render the plan after Accept moves the project out of the active slot (or-v9f.1).
// This is the post-delivery read regression behind the TestV20Loop plan/proof
// predicates: the flow decomposes the plan during the build (while active), then
// delivery empties the active slot — a strict-active resolver would then report
// "no current spec" for the code just built.
func TestPlanViewResolvesDeliveredProject(t *testing.T) {
	c, ctx := storeConductor(t)
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
	// Decompose + persist the plan while the project is active (as the build does).
	if _, err := c.PlanView(ctx); err != nil {
		t.Fatalf("plan (active): %v", err)
	}

	// Deliver: move the project out of the active slot exactly as the build path does.
	proj, _, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatalf("resolve active project: %v", err)
	}
	if err := c.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.Projects().SetStatus(ctx, proj.ID, "delivered")
	}); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	// Post-delivery: the active slot is empty, yet the plan must still resolve.
	pv, err := c.PlanView(ctx)
	if err != nil {
		t.Fatalf("post-delivery PlanView: %v", err)
	}
	if len(pv.Tasks) == 0 {
		t.Fatal("post-delivery plan has no tasks")
	}
}
