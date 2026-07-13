package orchestrator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
)

// TestApproveSpecPersistsRepoTarget (or-045a.7 DONE-WHEN a): a ratified
// path-shaped direction.repo_layout lands on the project row as the repo
// target; the managed-repo default records nothing.
func TestApproveSpecPersistsRepoTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "mech-pve-game")
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}
	answerFunctional(t, c, ctx)
	if err := c.RecordAnswer(ctx, "direction.repo_layout", target); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatal(err)
	}
	proj, _, err := c.Store().CurrentOrLastDeliveredProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if proj.RepoTarget != target {
		t.Fatalf("the ratified target must persist on the project row: %q", proj.RepoTarget)
	}

	// Negative: the managed-repo default (fallback or explicit) records NO target.
	c2, ctx2 := storeConductor(t)
	if _, err := c2.Submit(ctx2, flowIntent); err != nil {
		t.Fatal(err)
	}
	answerFunctional(t, c2, ctx2)
	if err := c2.RecordAnswer(ctx2, "direction.repo_layout", "managed-repo"); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.ApproveAssumptions(ctx2); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.ApproveSpec(ctx2); err != nil {
		t.Fatal(err)
	}
	proj2, _, err := c2.Store().CurrentOrLastDeliveredProjectSpec(ctx2)
	if err != nil {
		t.Fatal(err)
	}
	if proj2.RepoTarget != "" {
		t.Fatalf("managed-repo must record no target, got %q", proj2.RepoTarget)
	}

	// Negative: a non-path WORD ("standalone") is a phrase, not a location —
	// it must not be treated as a target either (the agent must elicit the
	// actual path).
	c3, ctx3 := storeConductor(t)
	if _, err := c3.Submit(ctx3, flowIntent); err != nil {
		t.Fatal(err)
	}
	answerFunctional(t, c3, ctx3)
	if err := c3.RecordAnswer(ctx3, "direction.repo_layout", "standalone"); err != nil {
		t.Fatal(err)
	}
	if _, err := c3.ApproveAssumptions(ctx3); err != nil {
		t.Fatal(err)
	}
	if _, err := c3.ApproveSpec(ctx3); err != nil {
		t.Fatal(err)
	}
	proj3, _, err := c3.Store().CurrentOrLastDeliveredProjectSpec(ctx3)
	if err != nil {
		t.Fatal(err)
	}
	if proj3.RepoTarget != "" {
		t.Fatalf("a non-path word must record no target, got %q", proj3.RepoTarget)
	}
}

// TestGreenfieldIntakeLeavesCwdUntouched (or-045a.7 DONE-WHEN b): a full
// greenfield intake — submit, type, goals, losses, ratify — writes NOTHING
// into the working directory. Every artifact lives in the context store.
func TestGreenfieldIntakeLeavesCwdUntouched(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	c, ctx := storeConductor(t) // the store lives in its own temp dir

	if _, err := c.Submit(ctx, largeHTTPIntent); err != nil {
		t.Fatal(err)
	}
	answerFunctional(t, c, ctx)
	if err := c.ProposeGoals(ctx, GoalsDoc{Goals: []string{"reliable time at scale"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RatifyGoals(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.RatifyGoalHazards(ctx,
		[]stpa.Loss{{ID: "GL1", Description: "clients act on wrong time"}},
		[]stpa.LossScenario{{ID: "GS1", Trigger: "clock skew", SustainingCondition: "no alarm", Loss: "GL1"}}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("the intake wrote into the cwd (the dogfood bug): %v", names)
	}
}
