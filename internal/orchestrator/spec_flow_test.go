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
