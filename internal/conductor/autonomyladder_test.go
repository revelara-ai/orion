package conductor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/actuation"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
)

// seedDeliveries writes n delivery rows for the project (each needs an epic).
func seedDeliveries(t *testing.T, store *contextstore.Store, projID string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		if err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
			specID, err := tx.Specs().CreateDraft(ctx, projID)
			if err != nil {
				return err
			}
			epicID, err := tx.Epics().Create(ctx, projID, specID, "epic", "hash")
			if err != nil {
				return err
			}
			_, err = tx.Deliveries().Create(ctx, epicID, "{}", "{}")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func ladderProject(t *testing.T) (*contextstore.Store, string) {
	t.Helper()
	store := openStore(t)
	var projID string
	if err := store.WithTx(context.Background(), func(tx *contextstore.Tx) error {
		id, err := tx.Projects().Create(context.Background(), "p", "intent", "greenfield")
		projID = id
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return store, projID
}

// (a) TrackRecord counts consecutive deliveries and an escalation RESETS it —
// autonomy is re-earned, never grandfathered.
func TestTrackRecordResetsOnEscalation(t *testing.T) {
	store, projID := ladderProject(t)
	ctx := context.Background()

	seedDeliveries(t, store, projID, 5)
	if n, err := store.TrackRecord(ctx, projID); err != nil || n != 5 {
		t.Fatalf("record after 5 clean deliveries: n=%d err=%v", n, err)
	}
	if err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
		_, err := tx.Escalations().CreateDetailed(ctx, projID, "", "bar refusal", "detail")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n, _ := store.TrackRecord(ctx, projID); n != 0 {
		t.Fatalf("an escalation must zero the ladder, got %d", n)
	}
	seedDeliveries(t, store, projID, 2)
	if n, _ := store.TrackRecord(ctx, projID); n != 2 {
		t.Fatalf("post-reset deliveries re-earn from zero, got %d", n)
	}
}

// (b) PolicyForRecord: Standard earns at the bar; Critical NEVER earns; bare
// PolicyFor stays false (back-compat).
func TestPolicyForRecordTierGating(t *testing.T) {
	if reliabilitytier.PolicyFor(reliabilitytier.Standard).AutonomyAllowed {
		t.Fatal("bare PolicyFor must stay human-mergeable (back-compat)")
	}
	if reliabilitytier.PolicyForRecord(reliabilitytier.Standard, 4).AutonomyAllowed {
		t.Fatal("below the bar (default 5) nothing is earned")
	}
	if !reliabilitytier.PolicyForRecord(reliabilitytier.Standard, 5).AutonomyAllowed {
		t.Fatal("Standard at the bar earns autonomy")
	}
	if !reliabilitytier.PolicyForRecord(reliabilitytier.Throwaway, 5).AutonomyAllowed {
		t.Fatal("Throwaway at the bar earns autonomy")
	}
	if reliabilitytier.PolicyForRecord(reliabilitytier.Critical, 1000).AutonomyAllowed {
		t.Fatal("Critical NEVER earns autonomy in this slice, regardless of record")
	}
	t.Setenv("ORION_AUTONOMY_BAR", "2")
	if !reliabilitytier.PolicyForRecord(reliabilitytier.Standard, 2).AutonomyAllowed {
		t.Fatal("ORION_AUTONOMY_BAR must tune the bar")
	}
}

// (e) Ladder consumption + the bead's scenario matrix: earned Standard
// grants; +1 escalation reverts to human-merge; Critical never grants; an
// engaged RedButton blocks an EARNED grant at the consumption gate.
func TestEarnedAutonomyScenarios(t *testing.T) {
	t.Setenv("ORION_POST_PROOF", "") // no explicit override — the ladder decides
	store, _ := ladderProject(t)
	ctx := context.Background()

	// The change flow's ladder lives on the reserved brownfield project.
	var bfID string
	_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
		id, err := tx.Projects().GetOrCreateReserved(ctx, contextstore.BrownfieldProjectName, "brownfield")
		bfID = id
		return err
	})
	seedDeliveries(t, store, bfID, 5)

	if !earnedPostProofAutonomy(ctx, store, string(reliabilitytier.Standard)) {
		t.Fatal("ladder-earned Standard must grant post-proof autonomy")
	}
	if earnedPostProofAutonomy(ctx, store, string(reliabilitytier.Critical)) {
		t.Fatal("Critical never auto-lands regardless of record")
	}

	// Engaged red button blocks the earned grant at the consumption gate.
	rb := actuation.RedButton{Path: filepath.Join(t.TempDir(), "red_button")}
	if err := rb.Engage(); err != nil {
		t.Fatal(err)
	}
	if actuation.AutonomousDeliverPermitted(rb, "deliver") {
		t.Fatal("an engaged RedButton must block an earned auto-land")
	}

	// +1 escalation reverts the same project to human-merge.
	_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
		_, err := tx.Escalations().CreateDetailed(ctx, bfID, "", "regression", "detail")
		return err
	})
	if earnedPostProofAutonomy(ctx, store, string(reliabilitytier.Standard)) {
		t.Fatal("one escalation must revert the project to human-merge")
	}

	// The explicit override still wins in both directions.
	t.Setenv("ORION_POST_PROOF", "auto")
	if g, ex := postProofAutonomy(); !g || !ex {
		t.Fatal("explicit auto opt-in must grant")
	}
	t.Setenv("ORION_POST_PROOF", "confirm")
	if g, ex := postProofAutonomy(); g || !ex {
		t.Fatal("explicit confirm opt-out must deny even with an earned record")
	}
}
