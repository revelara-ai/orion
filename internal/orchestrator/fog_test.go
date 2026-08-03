package orchestrator

import (
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// TestFogParkGraduateFlow (or-kjcm.5): a known-unknown parks without being
// phrased; graduation converts it atomically into precise ledger questions
// and removes the parked note.
func TestFogParkGraduateFlow(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}

	id, err := c.ParkFog(ctx, "something about auth, once the deployment target is known")
	if err != nil {
		t.Fatal(err)
	}
	fs, err := c.FogNotes(ctx)
	if err != nil || len(fs) != 1 {
		t.Fatalf("parked note must list: %v %+v", err, fs)
	}

	qids, err := c.GraduateFog(ctx, id, []FogQuestion{
		{Phase: "spec", Key: "grill.auth_model", Question: "Which auth model — session cookies or bearer tokens?", Severity: "advisory", Mode: "hitl"},
		{Phase: "spec", Question: "Does the deployment target terminate TLS?", Severity: "advisory", Mode: "research"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(qids) != 2 {
		t.Fatalf("expected 2 graduated questions, got %d", len(qids))
	}

	// The note left the fog ledger; the questions are real ledger rows.
	fs, err = c.FogNotes(ctx)
	if err != nil || len(fs) != 0 {
		t.Fatalf("graduated note must leave the fog list: %v %+v", err, fs)
	}
	qs, err := c.OpenQuestions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, q := range qs {
		if q.Origin == "fog" {
			found++
			if q.Phase != "spec" {
				t.Fatalf("graduated question shape: %+v", q)
			}
		}
	}
	if found != 2 {
		t.Fatalf("graduated questions must be open ledger rows, found %d: %+v", found, qs)
	}

	// Graduating the same note twice is an error (it already left).
	if _, err := c.GraduateFog(ctx, id, []FogQuestion{{Phase: "spec", Question: "again?", Severity: "advisory"}}); err == nil {
		t.Fatal("a graduated note must not graduate twice")
	}
}

// TestFogNeverBlocksRatification: open fog is advisory by construction — a
// spec ratifies over any amount of it.
func TestFogNeverBlocksRatification(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ParkFog(ctx, "a decision about rate limiting is coming"); err != nil {
		t.Fatal(err)
	}
	answerFunctional(t, c, ctx)
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatalf("open fog must never gate ratification: %v", err)
	}
}

// TestFogDismissAndValidation: dismissal needs a reason; graduation needs at
// least one phrased question; an empty park is refused.
func TestFogDismissAndValidation(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ParkFog(ctx, "   "); err == nil {
		t.Fatal("an empty note must not park")
	}
	id, err := c.ParkFog(ctx, "maybe multi-region, depends on the SLO answer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GraduateFog(ctx, id, nil); err == nil {
		t.Fatal("graduation without questions must be refused")
	}
	if err := c.DismissFog(ctx, id, ""); err == nil {
		t.Fatal("a dismissal without a reason must be refused")
	}
	if err := c.DismissFog(ctx, id, "single-region confirmed by the SLO answer"); err != nil {
		t.Fatal(err)
	}
	fs, err := c.FogNotes(ctx)
	if err != nil || len(fs) != 0 {
		t.Fatalf("dismissed note must leave the list: %v %+v", err, fs)
	}
	// The dismissal reason is durable.
	var f contextstore.FogNote
	if err := c.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		f, e = tx.Fog().Get(ctx, id)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if f.Status != "dismissed" || f.Resolution == "" {
		t.Fatalf("dismissal must record its reason: %+v", f)
	}
}
