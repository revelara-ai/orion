package orchestrator

import (
	"strings"
	"testing"
)

// TestDirectionDecisionChangesAnchorHash (or-045a.5 DONE-WHEN b): a recorded
// direction decision participates in the spec anchor hash — two otherwise
// identical specs with different directions anchor differently.
func TestDirectionDecisionChangesAnchorHash(t *testing.T) {
	build := func(protocol string) string {
		c, ctx := storeConductor(t)
		if _, err := c.Submit(ctx, flowIntent); err != nil {
			t.Fatal(err)
		}
		answerFunctional(t, c, ctx)
		if err := c.RecordAnswer(ctx, "direction.wire_protocol", protocol); err != nil {
			t.Fatal(err)
		}
		es, err := c.PreviewSpec(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return es.Hash
	}
	if build("http-json") == build("grpc") {
		t.Fatal("a direction decision must change the spec anchor hash")
	}
}

// TestOutOfCapabilityDirectionRefusesRatification (or-045a.5 DONE-WHEN c): a
// ratified direction the proof harness cannot prove produces a DETERMINISTIC
// refusal naming the gap and the explicit reduced-proof option — never a
// silent fallback to the http defaults (the dogfood Milestone-2 distortion).
// Acknowledging reduced proof clears exactly that gate.
func TestOutOfCapabilityDirectionRefusesRatification(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, gameIntent); err != nil {
		t.Fatal(err)
	}
	if err := c.SetProjectType(ctx, "game"); err != nil {
		t.Fatal(err)
	}
	if err := c.RecordAnswer(ctx, "direction.wire_protocol", "grpc"); err != nil {
		t.Fatal(err)
	}
	_, err := c.ApproveSpec(ctx)
	if err == nil {
		t.Fatal("an unprovable direction must refuse ratification")
	}
	msg := err.Error()
	if !strings.Contains(msg, "grpc") || !strings.Contains(msg, "acknowledge_reduced_proof") {
		t.Fatalf("the refusal must name the gap and the explicit option, got: %v", err)
	}

	// Acknowledging an un-gapped key is refused (the ack is scoped to real gaps).
	if err := c.AcknowledgeReducedProof(ctx, []string{"direction.language"}); err == nil {
		t.Fatal("acknowledging a key with no capability gap must be refused")
	}

	// The developer explicitly accepts reduced proof — the capability gate
	// clears; ratification proceeds to the NEXT gate (assumptions/cases), so
	// the error must no longer be the capability refusal.
	if err := c.AcknowledgeReducedProof(ctx, []string{"direction.wire_protocol"}); err != nil {
		t.Fatal(err)
	}
	_, err = c.ApproveSpec(ctx)
	if err == nil {
		t.Fatal("later floor gates still apply")
	}
	if strings.Contains(err.Error(), "acknowledge_reduced_proof") {
		t.Fatalf("the capability gate must be cleared by the acknowledgment, got: %v", err)
	}

	// Negative: a provable direction never triggers the refusal.
	c2, ctx2 := storeConductor(t)
	if _, err := c2.Submit(ctx2, flowIntent); err != nil {
		t.Fatal(err)
	}
	answerFunctional(t, c2, ctx2)
	if err := c2.RecordAnswer(ctx2, "direction.wire_protocol", "http-json"); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.ApproveAssumptions(ctx2); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.ApproveSpec(ctx2); err != nil {
		t.Fatalf("a provable direction must ratify cleanly, got: %v", err)
	}
}
