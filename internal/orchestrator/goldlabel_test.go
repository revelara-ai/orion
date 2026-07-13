package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// goldFixture drives a spec to the brink of ratification with provenance set.
func goldFixture(t *testing.T) (*Conductor, *contextstore.Store, context.Context) {
	t.Helper()
	store, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	c := NewWithStore(store)
	c.SetProducerProvenance("anthropic/claude-test", "checklist-v3")
	ctx := context.Background()
	if _, err := c.Submit(ctx, "Build an HTTP service that returns the current time."); err != nil {
		t.Fatal(err)
	}
	for _, a := range [][2]string{{"response_format", "json"}, {"timezone", "UTC"}, {"port", "8080"}, {"route", "/time"}} {
		if err := c.RecordAnswer(ctx, a[0], a[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.AddRequirement(ctx, spec.Requirement{
		Source: completeness.DimFunctional,
		Text:   "GET /time returns an RFC3339 time key",
		Cases: []spec.BehavioralCase{{
			Request: spec.RequestShape{Method: "GET", Path: "/time"},
			Expect: spec.ExpectShape{Status: 200, ContentType: "application/json",
				Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyRFC3339, Key: "time"}}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	return c, store, ctx
}

func labelsByKind(t *testing.T, store *contextstore.Store, ctx context.Context, c *Conductor) map[string][]contextstore.GoldLabel {
	t.Helper()
	proj, _, err := store.CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	labels, err := store.ListGoldLabels(ctx, proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]contextstore.GoldLabel{}
	for _, l := range labels {
		out[l.RatificationKind] = append(out[l.RatificationKind], l)
	}
	return out
}

// TestApproveAssumptionsEmitsOneLabelPerAssumption (bead-named).
func TestApproveAssumptionsEmitsOneLabelPerAssumption(t *testing.T) {
	c, store, ctx := goldFixture(t)
	approved, err := c.ApproveAssumptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(approved) < 2 {
		t.Fatalf("fixture must have MULTIPLE fallback assumptions (one-label-per must be distinguishable from one-label-total), got %d", len(approved))
	}
	byKind := labelsByKind(t, store, ctx, c)
	if got := len(byKind["assumption"]); got != len(approved) {
		t.Fatalf("one label per assumption: got %d labels for %d approvals", got, len(approved))
	}
	for _, l := range byKind["assumption"] {
		if l.Outcome != "accept" || l.ModelID != "anthropic/claude-test" {
			t.Fatalf("assumption label malformed: %+v", l)
		}
	}
}

// TestApproveSpecEmitsGoldLabelWithProvenance (bead-named).
func TestApproveSpecEmitsGoldLabelWithProvenance(t *testing.T) {
	c, store, ctx := goldFixture(t)
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatal(err)
	}
	es, err := c.ApproveSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byKind := labelsByKind(t, store, ctx, c)
	rat := byKind["spec_ratification"]
	if len(rat) != 1 {
		t.Fatalf("exactly one ratification label, got %d", len(rat))
	}
	l := rat[0]
	if l.Outcome != "accept" || l.ArtifactHash != es.Hash {
		t.Fatalf("label must anchor the ratified hash: %+v", l)
	}
	if l.ModelID != "anthropic/claude-test" || l.ProducerVersion != "checklist-v3" {
		t.Fatalf("producer provenance lost: %+v", l)
	}
}

// TestEscalationResolutionEmitsGoldLabel (bead-named): the same-tx helper the
// CLI uses resolves + labels atomically; unknown provenance is EXPLICIT.
func TestEscalationResolutionEmitsGoldLabel(t *testing.T) {
	_, store, ctx := goldFixture(t)
	proj, _, err := store.CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var escID string
	if err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
		id, e := tx.Escalations().CreateDetailed(ctx, proj.ID, "", "needs a human", "detail")
		escID = id
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.ResolveEscalationGold(ctx, escID, "fixed by hand", "human/cli", "")
	}); err != nil {
		t.Fatal(err)
	}
	labels, err := store.ListGoldLabels(ctx, proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range labels {
		if l.RatificationKind == "escalation" && l.ArtifactHash == escID {
			found = true
			if l.Outcome != "resolved" || l.ModelID != "human/cli" || l.ProducerVersion != "unknown" {
				t.Fatalf("escalation label malformed: %+v", l)
			}
		}
	}
	if !found {
		t.Fatal("resolution did not capture a gold label")
	}
}

// TestGoldLabelWriteIsAtomicWithRatificationTx (bead-named): a rolled-back tx
// leaves NEITHER the resolution nor the label.
func TestGoldLabelWriteIsAtomicWithRatificationTx(t *testing.T) {
	_, store, ctx := goldFixture(t)
	proj, _, err := store.CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var escID string
	if err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
		id, e := tx.Escalations().CreateDetailed(ctx, proj.ID, "", "needs a human", "detail")
		escID = id
		return e
	}); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err = store.WithTx(ctx, func(tx *contextstore.Tx) error {
		if e := tx.ResolveEscalationGold(ctx, escID, "half-done", "m", "v"); e != nil {
			return e
		}
		return boom // force rollback AFTER both writes
	})
	if !errors.Is(err, boom) {
		t.Fatalf("tx must surface the rollback cause: %v", err)
	}
	labels, _ := store.ListGoldLabels(ctx, proj.ID)
	for _, l := range labels {
		if l.RatificationKind == "escalation" {
			t.Fatalf("rolled-back tx leaked a gold label: %+v", l)
		}
	}
	var resolved bool
	_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
		e, err := tx.Escalations().Get(ctx, escID)
		if err != nil {
			return err
		}
		resolved = e.Resolved
		return nil
	})
	if resolved {
		t.Fatal("rolled-back tx leaked the resolution")
	}
}
