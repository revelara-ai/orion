package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

// fakeGrill returns one keyed open-ended question and records that it ran.
func fakeGrill(ran *bool) GrillAgent {
	return func(_ context.Context, _ string, _ map[string]string, _ []completeness.OpenDecision) ([]completeness.OpenDecision, error) {
		*ran = true
		return []completeness.OpenDecision{{Key: "grill.audience", Question: "Who is this for, and what does failure cost them?"}}, nil
	}
}

// TestGrillDrivesForLargeScaleIntent (or-045a.2): a LARGE-scale intent enters
// grill-driven intake with NO env var — the grill's questions lead and the
// reliability floor follows (demoted, never dropped). A standard-scale intent
// keeps the V2 checklist driver: the grill is not even consulted.
func TestGrillDrivesForLargeScaleIntent(t *testing.T) {
	c, ctx := storeConductor(t)
	ran := false
	c.SetGrillAgent(fakeGrill(&ran))

	conf, err := c.Submit(ctx, gameIntent) // scale=large (or-045a.1)
	if err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("a large-scale intent must consult the grill without ORION_ELICITATION=grill")
	}
	if len(conf.OpenDecisions) == 0 || conf.OpenDecisions[0].Key != "grill.audience" {
		t.Fatalf("the grill's questions must LEAD, got %+v", conf.OpenDecisions)
	}
	// The floor still follows (universal reliability dimensions present).
	keys := map[string]bool{}
	for _, d := range conf.OpenDecisions {
		keys[d.Key] = true
	}
	if !keys["slo_targets"] || !keys["security_model"] {
		t.Fatalf("the reliability floor must never be dropped, got %v", keys)
	}

	// Negative (no over-reach): a standard-scale intent never consults the grill.
	c2, ctx2 := storeConductor(t)
	ran2 := false
	c2.SetGrillAgent(fakeGrill(&ran2))
	if _, err := c2.Submit(ctx2, flowIntent); err != nil {
		t.Fatal(err)
	}
	if ran2 {
		t.Fatal("a standard-scale intent must keep the V2 checklist driver (grill not consulted)")
	}
}

// TestGoalsProposeRatifyRoundTrip (or-045a.2): the goals document round-trips
// through the context store — proposed as a draft, ratified with a content
// hash, retrievable by the conductor. Never a loose file.
func TestGoalsProposeRatifyRoundTrip(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, gameIntent); err != nil {
		t.Fatal(err)
	}

	// Ratifying before any proposal is refused.
	if _, err := c.RatifyGoals(ctx); err == nil {
		t.Fatal("ratify_goals before propose_goals must be refused")
	}

	doc := GoalsDoc{
		Goals:           []string{"Pure co-op PvE extraction survival", "RL-driven mech movement that adapts to damage"},
		NonGoals:        []string{"No PvP"},
		SuccessCriteria: []string{"Sub-16ms inference budget at 60Hz"},
	}
	if err := c.ProposeGoals(ctx, doc); err != nil {
		t.Fatal(err)
	}
	// Draft is retrievable but not yet ratified.
	got, status, _, err := c.Goals(ctx)
	if err != nil || status != "drafting" {
		t.Fatalf("draft goals: status=%q err=%v", status, err)
	}
	if len(got.Goals) != 2 || got.NonGoals[0] != "No PvP" {
		t.Fatalf("draft content mangled: %+v", got)
	}

	hash, err := c.RatifyGoals(ctx)
	if err != nil || hash == "" {
		t.Fatalf("ratify: hash=%q err=%v", hash, err)
	}
	got2, status2, hash2, err := c.Goals(ctx)
	if err != nil || status2 != "ratified" || hash2 != hash {
		t.Fatalf("ratified goals: status=%q hash=%q/%q err=%v", status2, hash, hash2, err)
	}
	if got2.SuccessCriteria[0] != "Sub-16ms inference budget at 60Hz" {
		t.Fatalf("ratified content mangled: %+v", got2)
	}

	// Negative: an empty proposal is refused (a goals doc with nothing in it
	// cannot steer anything).
	if err := c.ProposeGoals(ctx, GoalsDoc{}); err == nil {
		t.Fatal("an empty goals proposal must be refused")
	}
}

// TestGrillModeKeepsFloorEnforcement (or-045a.2 DONE-WHEN d): with the grill
// leading a large intent, unanswered universal decisions still block
// ratification (the assumption gate) — the floor is demoted, never waived.
func TestGrillModeKeepsFloorEnforcement(t *testing.T) {
	c, ctx := storeConductor(t)
	ran := false
	c.SetGrillAgent(fakeGrill(&ran))
	if _, err := c.Submit(ctx, gameIntent); err != nil {
		t.Fatal(err)
	}
	if err := c.SetProjectType(ctx, "game"); err != nil { // or-045a.1: resolve the type first
		t.Fatal(err)
	}
	// An UNANSWERED grill question blocks too — it was asked because the answer
	// changes what gets built (no silent dropping).
	if _, err := c.ApproveSpec(ctx); err == nil || !strings.Contains(err.Error(), "grill.audience") {
		t.Fatalf("an unanswered grill question must block ratification, got: %v", err)
	}
	// Answer it; the UNIVERSAL floor still blocks (assumption gate / zero-case
	// gate) — demoted, never waived.
	if err := c.RecordAnswer(ctx, "grill.audience", "co-op PvE players"); err != nil {
		t.Fatal(err)
	}
	_, err := c.ApproveSpec(ctx)
	if err == nil {
		t.Fatal("ratification must still be blocked by the unanswered floor")
	}
	if !strings.Contains(err.Error(), "assumption") && !strings.Contains(err.Error(), "case") {
		t.Fatalf("the block must come from the floor gates, got: %v", err)
	}
}
