package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

func ratifiedNonGoals(t *testing.T, c *Conductor, ctx context.Context) {
	t.Helper()
	if err := c.ProposeGoals(ctx, GoalsDoc{
		Goals:          []string{"a time service for the team"},
		NonGoalDetails: []NonGoal{{Statement: "no user accounts or authentication", Rationale: "internal network only; auth doubles the surface"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RatifyGoals(ctx); err != nil {
		t.Fatal(err)
	}
}

func scopeReq(text string) spec.Requirement {
	return spec.Requirement{
		Source: completeness.DimFunctional,
		Text:   text,
		Cases: []spec.BehavioralCase{{
			Request: spec.RequestShape{Method: "GET", Path: "/time"},
			Expect: spec.ExpectShape{Status: 200, ContentType: "application/json",
				Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyPresent, Key: "time"}}},
		}},
	}
}

// TestScopeCheckRefusesNonGoalCrossing (or-kjcm.6): with ratified non-goals
// and a wired checker, a conflicting requirement is refused with the crossed
// non-goal named; scope_override records it as an audited scope change.
func TestScopeCheckRefusesNonGoalCrossing(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}
	ratifiedNonGoals(t, c, ctx)
	c.SetScopeChecker(func(_ context.Context, req string, ngs []NonGoal) (*ScopeConflict, error) {
		if strings.Contains(req, "log in") {
			return &ScopeConflict{NonGoal: ngs[0].Statement, Reason: "a login flow is authentication"}, nil
		}
		return nil, nil
	})

	err := c.AddRequirement(ctx, scopeReq("users log in before seeing the time"))
	if err == nil {
		t.Fatal("a requirement crossing a ratified non-goal must be refused")
	}
	if !strings.Contains(err.Error(), "no user accounts or authentication") || !strings.Contains(err.Error(), "scope_override") {
		t.Fatalf("the refusal must name the non-goal and the explicit override path: %v", err)
	}

	// An in-scope requirement records untouched.
	if err := c.AddRequirement(ctx, scopeReq("the time endpoint returns JSON")); err != nil {
		t.Fatalf("in-scope requirement must record: %v", err)
	}

	// The developer's explicit confirmation crosses the boundary — audited.
	if err := c.AddRequirementScoped(ctx, scopeReq("users log in before seeing the time"), true); err != nil {
		t.Fatalf("scope_override must record the requirement: %v", err)
	}
	labels, err := c.Store().ListGoldLabels(ctx, c.ProjectID())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range labels {
		if l.RatificationKind == "scope_change" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a scope override must gold-label the scope change: %+v", labels)
	}
}

// TestScopeCheckFailsOpen: a broken judge never blocks requirement capture.
func TestScopeCheckFailsOpen(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}
	ratifiedNonGoals(t, c, ctx)
	c.SetScopeChecker(func(context.Context, string, []NonGoal) (*ScopeConflict, error) {
		return nil, errors.New("judge offline")
	})
	if err := c.AddRequirement(ctx, scopeReq("users log in before seeing the time")); err != nil {
		t.Fatalf("a broken scope judge must fail open: %v", err)
	}
}

// TestScopeCheckSkipsWithoutRatifiedNonGoals: draft goals (or none) leave the
// checker unconsulted — only a RATIFIED ledger is enforced.
func TestScopeCheckSkipsWithoutRatifiedNonGoals(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}
	called := false
	c.SetScopeChecker(func(context.Context, string, []NonGoal) (*ScopeConflict, error) {
		called = true
		return nil, nil
	})
	if err := c.AddRequirement(ctx, scopeReq("the time endpoint returns JSON")); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("no ratified non-goals — the checker must not be consulted")
	}
}

// TestDestinationSteersGrillWithoutGoalsCeremony (or-kjcm.6): a standard-scale
// intake records a one-line destination; GrillContext feeds it to the grill
// even though nothing is ratified. Ratified goals win over the bare line.
func TestDestinationSteersGrillWithoutGoalsCeremony(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}
	if c.GrillContext(ctx) != "" {
		t.Fatal("no destination, no goals — nothing to steer by yet")
	}
	if err := c.SetDestination(ctx, "the team never asks for the time in slack again"); err != nil {
		t.Fatal(err)
	}
	gc := c.GrillContext(ctx)
	if !strings.HasPrefix(gc, "Destination: ") || !strings.Contains(gc, "slack") {
		t.Fatalf("the destination line must steer the grill: %q", gc)
	}
	// GoalsSummary keeps its ratified-only contract.
	if c.GoalsSummary(ctx) != "" {
		t.Fatal("a draft destination must not leak into GoalsSummary")
	}
	// Ratifying full goals supersedes the bare line.
	if _, err := c.RatifyGoals(ctx); err != nil {
		t.Fatal(err)
	}
	if gc := c.GrillContext(ctx); !strings.Contains(gc, "Destination:") || c.GoalsSummary(ctx) == "" {
		t.Fatalf("ratified goals must carry the destination: %q", gc)
	}
	// A ratified document refuses the shortcut editor.
	if err := c.SetDestination(ctx, "something else"); err == nil {
		t.Fatal("set_destination must not silently edit a ratified document")
	}
}

// TestNonGoalRationaleSurvivesRoundTrip: structured entries persist and render
// with their why; the legacy statement list stays in sync.
func TestNonGoalRationaleSurvivesRoundTrip(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}
	ratifiedNonGoals(t, c, ctx)
	doc, status, _, err := c.Goals(ctx)
	if err != nil || status != "ratified" {
		t.Fatalf("goals: %v %s", err, status)
	}
	ngs := doc.NonGoalLedger()
	if len(ngs) != 1 || ngs[0].Rationale == "" {
		t.Fatalf("rationale lost: %+v", ngs)
	}
	if len(doc.NonGoals) != 1 || doc.NonGoals[0] != ngs[0].Statement {
		t.Fatalf("legacy statement list out of sync: %+v", doc.NonGoals)
	}
	if r := doc.Render(); !strings.Contains(r, "why: internal network only") {
		t.Fatalf("render must carry the why: %s", r)
	}
	var _ = contextstore.ErrNotFound // keep the import honest if assertions change
}
