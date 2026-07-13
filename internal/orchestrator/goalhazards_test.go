package orchestrator

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
)

// largeHTTPIntent: explicit http signal + explicit large signal — the shape
// that must go through the full goals→losses→spec chain.
const largeHTTPIntent = "Build an HTTP service that returns the current time. I expect this to be a large project."

// TestRatifyGoalHazardsPersistsModel (or-045a.3 DONE-WHEN a+b): losses and
// scenarios ratified at GOAL altitude persist via stpa.Save — the model
// build.go Loads is the goal-derived one, NOT the skeleton fallback. The
// control side rides the skeleton's closed loop until build time (no code
// exists yet); the questionnaire's gate order is enforced internally.
func TestRatifyGoalHazardsPersistsModel(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, gameIntent); err != nil {
		t.Fatal(err)
	}
	losses := []stpa.Loss{{ID: "GL1", Description: "players lose a raid to an unfair mech glitch (trust loss)"}}
	scenarios := []stpa.LossScenario{{ID: "GS1", Trigger: "inference latency exceeds the 16ms tick budget", SustainingCondition: "no IK fallback engaged", Loss: "GL1"}}

	// Losses are drafted FROM the ratified goals — no goals, no hazard model.
	if err := c.RatifyGoalHazards(ctx, losses, scenarios); err == nil {
		t.Fatal("ratifying goal hazards without ratified goals must be refused")
	}
	if err := c.ProposeGoals(ctx, GoalsDoc{Goals: []string{"uncanny RL mech movement"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RatifyGoals(ctx); err != nil {
		t.Fatal(err)
	}

	// A scenario referencing an unknown loss is refused (coherence).
	bad := []stpa.LossScenario{{ID: "GSX", Trigger: "t", SustainingCondition: "s", Loss: "NOPE"}}
	if err := c.RatifyGoalHazards(ctx, losses, bad); err == nil {
		t.Fatal("a scenario referencing an unknown loss must be refused")
	}
	// Empty losses are refused (the questionnaire's own gate).
	if err := c.RatifyGoalHazards(ctx, nil, scenarios); err == nil {
		t.Fatal("empty losses must be refused")
	}

	if err := c.RatifyGoalHazards(ctx, losses, scenarios); err != nil {
		t.Fatal(err)
	}
	proj, _, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	m, found, err := stpa.Load(ctx, c.Store(), proj.ID)
	if err != nil || !found {
		t.Fatalf("the ratified model must persist where build.go Loads it: found=%v err=%v", found, err)
	}
	if len(m.Losses) != 1 || m.Losses[0].ID != "GL1" {
		t.Fatalf("the loaded model must carry the GOAL losses, not the skeleton's: %+v", m.Losses)
	}
	if len(m.Scenarios) != 1 || m.Scenarios[0].Loss != "GL1" {
		t.Fatalf("goal scenarios must persist: %+v", m.Scenarios)
	}
	// Questionnaire-complete: the control loop is closed (skeleton structure).
	if len(m.Structure.Actions) == 0 || len(m.UCAs) == 0 {
		t.Fatalf("the model must be questionnaire-complete: %+v", m)
	}
	// The generic UCA's loss refs are remapped onto the ratified losses.
	for _, u := range m.UCAs {
		for _, ref := range u.LossRefs {
			if ref != "GL1" {
				t.Fatalf("UCA loss refs must reference the ratified losses, got %q", ref)
			}
		}
	}
}

// TestApproveSpecBlocksLargeWithoutHazardModel (or-045a.3 DONE-WHEN c+d): a
// LARGE-scale project cannot ratify its spec until goal hazards are ratified;
// a standard-scale project keeps the frictionless path.
func TestApproveSpecBlocksLargeWithoutHazardModel(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, largeHTTPIntent); err != nil {
		t.Fatal(err)
	}
	answerFunctional(t, c, ctx)
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := c.ApproveSpec(ctx)
	if err == nil {
		t.Fatal("a large project without a ratified hazard model must not ratify")
	}
	if !strings.Contains(err.Error(), "loss") {
		t.Fatalf("the block must point at the missing loss analysis, got: %v", err)
	}

	// Ratify goals + goal hazards → the spec ratifies.
	if err := c.ProposeGoals(ctx, GoalsDoc{Goals: []string{"reliable current-time service at scale"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RatifyGoals(ctx); err != nil {
		t.Fatal(err)
	}
	losses := []stpa.Loss{{ID: "GL1", Description: "clients act on a wrong time"}}
	scenarios := []stpa.LossScenario{{ID: "GS1", Trigger: "clock skew", SustainingCondition: "no drift alarm", Loss: "GL1"}}
	if err := c.RatifyGoalHazards(ctx, losses, scenarios); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatalf("with goals + hazards ratified the large spec must ratify: %v", err)
	}

	// Negative (DONE-WHEN c): the standard-scale path has NO new friction.
	c2, ctx2 := storeConductor(t)
	if _, err := c2.Submit(ctx2, flowIntent); err != nil {
		t.Fatal(err)
	}
	answerFunctional(t, c2, ctx2)
	if _, err := c2.ApproveAssumptions(ctx2); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.ApproveSpec(ctx2); err != nil {
		t.Fatalf("standard-scale must ratify without any hazard-model friction: %v", err)
	}
}
