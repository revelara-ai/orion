package conductor

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

func rejectReportFor(t *testing.T) proof.Report {
	t.Helper()
	return proof.Report{Outcome: truthalign.Outcome{
		Verdict:    truthalign.Reject,
		Dissenting: []string{"behavioral: 1 case failing"},
	}}
}

// TestFailureModeRecordedFromFailingObligation (or-gb1.3 acceptance): the
// PERSIST half — a task-level Reject records a failure_modes row whose key
// derives from the failing obligation + dissenting mode (not just the old
// integration-reproof site).
func TestFailureModeRecordedFromFailingObligation(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	var projID string
	if err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
		pid, e := tx.Projects().Create(ctx, "p", "intent", "http-service")
		projID = pid
		return e
	}); err != nil {
		t.Fatal(err)
	}
	analysis := "Proof verdict: Reject.\nFAILED case case-abc123: expected 200, got 500"
	if err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
		_, e := tx.FailureModes().Record(ctx, projID,
			failureCategory(rejectReportFor(t)), clip("GET /time listens on port 8080 and returns RFC3339", 60), clip(failureSymptom(analysis), 80))
		return e
	}); err != nil {
		t.Fatal(err)
	}
	var rows []contextstore.FailureMode
	_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
		fs, e := tx.FailureModes().ListForProject(ctx, projID)
		rows = fs
		return e
	})
	if len(rows) != 1 {
		t.Fatalf("want 1 failure mode, got %d", len(rows))
	}
	if rows[0].Category != "behavioral" || !strings.Contains(rows[0].ComponentType, "GET /time") || !strings.Contains(rows[0].SymptomClass, "case-abc123") {
		t.Fatalf("the row must derive from mode+obligation+symptom, got %+v", rows[0])
	}
}

// TestKnownFailureModesReachGenerationContext (or-gb1.3 acceptance): the
// CONSULT half — recorded failure modes render into the generation-context
// section at task start; an empty history renders nothing.
func TestKnownFailureModesReachGenerationContext(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	if got := knownFailureModesSection(ctx, store, nil); got != "" {
		t.Fatalf("no active project must yield no section, got %q", got)
	}
	var projID string
	_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
		pid, e := tx.Projects().Create(ctx, "p", "intent", "http-service")
		projID = pid
		return e
	})
	_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
		_, e := tx.FailureModes().Record(ctx, projID, "behavioral", "GET /time handler", "case-abc123 expected 200 got 500")
		return e
	})
	got := knownFailureModesSection(ctx, store, nil)
	if !strings.Contains(got, "KNOWN FAILURE MODES") || !strings.Contains(got, "case-abc123") || !strings.Contains(got, "do NOT repeat") {
		t.Fatalf("the section must render the recorded mode, got:\n%s", got)
	}
}
