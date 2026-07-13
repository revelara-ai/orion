package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// seedActiveProject creates one ACTIVE project directly (bypassing Submit's
// classification) so a test can simulate a pre-rail fossil row.
func seedActiveProject(t *testing.T, c *Conductor, name, intent, projectType string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := c.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		id, e = tx.Projects().Create(ctx, name, intent, projectType)
		if e != nil {
			return e
		}
		_, e = tx.Specs().CreateDraft(ctx, id)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestStaleTypeDemotedOnLoad (or-hn15.3 DONE-WHEN c): a pre-rail fossil — a row
// typed http-service whose intent carries no HTTP signal — is re-examined on
// load: the gate demotes to unclassified, so the HTTP port/route checklist does
// NOT resurface and ratification blocks on the type.
func TestStaleTypeDemotedOnLoad(t *testing.T) {
	c, ctx := storeConductor(t)
	// The fossil: the old default typed a game intent http-service.
	seedActiveProject(t, c, "fossil", "build a game like Arc Raiders, pure PvE", "http-service")

	sv, err := c.SpecView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range sv.OpenDecisions {
		if d.Key == "port" || d.Key == "route" || d.Key == "response_format" {
			t.Fatalf("a signal-less http-service fossil must not resurrect the HTTP checklist, got %q", d.Key)
		}
	}
	// Ratification blocks on the unresolved (demoted) type.
	if _, err := c.ApproveSpec(ctx); err == nil || !strings.Contains(strings.ToLower(err.Error()), "type") {
		t.Fatalf("the demoted type must block ratification on the type, got: %v", err)
	}
}

// TestSignalBearingTypeNotDemoted (or-hn15.3 DONE-WHEN d): a row whose intent
// DOES signal http-service is unaffected — its HTTP checklist stands.
func TestSignalBearingTypeNotDemoted(t *testing.T) {
	c, ctx := storeConductor(t)
	seedActiveProject(t, c, "real-http", "build an HTTP service that returns the current time", "http-service")

	sv, err := c.SpecView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var hasHTTP bool
	for _, d := range sv.OpenDecisions {
		if d.Key == "port" || d.Key == "route" {
			hasHTTP = true
		}
	}
	if !hasHTTP {
		t.Fatalf("a signal-bearing http-service must keep its HTTP checklist, got %+v", sv.OpenDecisions)
	}
}

// TestQueuedBehindStaleRefuses (or-hn15.3 DONE-WHEN b): when the session's
// submitted intent is queued behind a DIFFERENT active project, spec-flow tools
// refuse — naming both — instead of silently editing the active one. It clears
// once the queue advances.
func TestQueuedBehindStaleRefuses(t *testing.T) {
	c, ctx := storeConductor(t)
	// A stale active project holds the slot.
	if _, err := c.Submit(ctx, "Build an HTTP service that returns the current time."); err != nil {
		t.Fatal(err)
	}
	// This session's real intent queues behind it.
	conf, err := c.Submit(ctx, "Build a PvE game like Arc Raiders.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conf.Message, "Queued") {
		t.Fatalf("precondition: the second intent must queue, got %q", conf.Message)
	}

	// A read (check_completeness → SpecView) refuses, naming both intents.
	_, err = c.SpecView(ctx)
	if err == nil || !strings.Contains(err.Error(), "QUEUED") ||
		!strings.Contains(err.Error(), "game") || !strings.Contains(err.Error(), "time") {
		t.Fatalf("SpecView must refuse naming both projects while queued behind a stale active one, got: %v", err)
	}
	// A mutation refuses too.
	if err := c.ProposeGoals(ctx, GoalsDoc{Goals: []string{"x"}}); err == nil || !strings.Contains(err.Error(), "QUEUED") {
		t.Fatalf("a mutation must refuse while the intent is queued, got: %v", err)
	}

	// Resolve the queue: abandon the stale active project so the game activates.
	proj, _, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.Projects().SetStatus(ctx, proj.ID, "abandoned")
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Store().ActivateNextQueued(ctx); err != nil {
		t.Fatal(err)
	}
	// Now the game is active — no conflict.
	sv, err := c.SpecView(ctx)
	if err != nil {
		t.Fatalf("once the queue advances the conflict must clear: %v", err)
	}
	if !strings.Contains(sv.Intent, "game") {
		t.Fatalf("the resolved project must now be the game, got %q", sv.Intent)
	}
}
