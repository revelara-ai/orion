package orchestrator

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
)

// TestLargeDirectionSurvivesReload (or-hn15.4 DONE-WHEN b): a large project
// resumed in a FRESH conductor (a later session, default gate) rebuilds the gate
// scale-aware, so the direction rail is still in the checklist — matching type
// alone (the bug) would drop it and default the language to Go.
func TestLargeDirectionSurvivesReload(t *testing.T) {
	st := storeOnly(t)
	// Session 1: submit a large http-service intent (direction rides at large).
	c1 := NewWithStore(st)
	if _, err := c1.Submit(t.Context(), "Build an HTTP service that returns the current time. I expect this to be a large project."); err != nil {
		t.Fatal(err)
	}
	sv1, err := c1.SpecView(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !hasDirection(sv1) {
		t.Fatal("precondition: a large intake must raise the direction rail")
	}

	// Session 2: a brand-new conductor (default http-service/standard gate) over
	// the same store. Matching type alone (both http-service) would skip the
	// rebuild and drop direction — the reload bug.
	c2 := NewWithStore(st)
	sv2, err := c2.SpecView(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !hasDirection(sv2) {
		t.Fatalf("a resumed large project must keep the direction rail across sessions, got %+v", sv2.OpenDecisions)
	}
}

// TestLargeRecallNoFalseTamper (or-hn15.4 DONE-WHEN c): a large project ratified
// in session 1 recalls in session 2 with NO anchor mismatch — the reloaded gate
// includes the direction dimension the spec was compiled under.
func TestLargeRecallNoFalseTamper(t *testing.T) {
	st := storeOnly(t)
	c1 := NewWithStore(st)
	ctx := t.Context()
	if _, err := c1.Submit(ctx, "Build an HTTP service that returns the current time. I expect this to be a large project."); err != nil {
		t.Fatal(err)
	}
	// The large flow: ratified goals + goal-altitude losses precede the spec.
	if err := c1.ProposeGoals(ctx, GoalsDoc{Goals: []string{"reliable time at scale"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c1.RatifyGoals(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c1.RatifyGoalHazards(ctx,
		[]stpa.Loss{{ID: "GL1", Description: "clients act on wrong time"}},
		[]stpa.LossScenario{{ID: "GS1", Trigger: "clock skew", SustainingCondition: "no alarm", Loss: "GL1"}}); err != nil {
		t.Fatal(err)
	}
	answerFunctional(t, c1, ctx)
	// Answer the direction family explicitly (a large intake asks it).
	for _, kv := range [][2]string{
		{"direction.stack", "single service"}, {"direction.language", "go"},
		{"direction.engine", "none"}, {"direction.wire_protocol", "http-json"},
		{"direction.repo_layout", "managed-repo"},
	} {
		if err := c1.RecordAnswer(ctx, kv[0], kv[1]); err != nil {
			t.Fatalf("answer %s: %v", kv[0], err)
		}
	}
	if _, err := c1.ApproveAssumptions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c1.ApproveSpec(ctx); err != nil {
		t.Fatalf("ratify: %v", err)
	}

	// Session 2: recall must NOT report a tamper (the anchor recompiles cleanly).
	c2 := NewWithStore(st)
	if _, err := c2.RecallSpec(ctx); err != nil {
		t.Fatalf("a large project must recall without a false tamper across sessions: %v", err)
	}
}

func hasDirection(sv SpecView) bool {
	for _, d := range sv.OpenDecisions {
		if strings.HasPrefix(d.Key, "direction.") {
			return true
		}
	}
	return false
}

func storeOnly(t *testing.T) *contextstore.Store {
	t.Helper()
	st, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}
