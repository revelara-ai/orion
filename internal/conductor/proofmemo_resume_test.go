package conductor

import (
	"strings"
	"sync"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// TestPersistedProofSkipsReproveOnRerun (or-v9f.6): re-running against the same
// store reuses the persisted proof for the unchanged artifact instead of
// re-running the expensive behavioral+empirical+hazard proof — the practical
// mid-run resume (fix an escalation, re-run, proven clusters skip). The fixture
// generates deterministically, so the second run's artifact hash matches the
// first and the cross-run memo hits.
func TestPersistedProofSkipsReproveOnRerun(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the full build+prove pipeline twice; skipped in -short")
	}
	oc, ctx := ratifiedTimeService(t)

	// First run: proves and persists into the shared store.
	if _, err := BuildAndProve(ctx, oc.Store(), nil, nil, nil, ""); err != nil {
		t.Fatalf("first build: %v", err)
	}

	// A delivered project leaves the active slot (or-v9f.1 queue lifecycle); the
	// real resume scenario re-runs a project that ESCALATED and stayed active. To
	// isolate the memo behavior under test, reactivate the spec's project so the
	// second run recalls the identical anchor.
	if err := oc.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		p, e := tx.Projects().Latest(ctx)
		if e != nil {
			return e
		}
		return tx.Projects().SetStatus(ctx, p.ID, "active")
	}); err != nil {
		t.Fatalf("reactivate project: %v", err)
	}

	// Second run on the SAME store: a fresh in-memory proof cache, but the
	// persisted memo should carry the verdict for the unchanged fixture artifact.
	var mu sync.Mutex
	var reused bool
	var proveRan bool
	sink := PhaseSink(func(e PhaseEvent) {
		if e.Phase != "Prove" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(e.Detail, "persisted proof") {
			reused = true
		}
		if e.Status == PhaseRunning && strings.Contains(e.Detail, "behavioral") {
			proveRan = true
		}
	})
	res, err := BuildAndProve(ctx, oc.Store(), nil, nil, sink, "")
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if res.Verdict != "Accept" {
		t.Fatalf("the re-run must still prove Accept from the persisted verdict, got %q", res.Verdict)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reused {
		t.Fatal("the re-run must reuse the persisted proof (no 'persisted proof' phase event seen)")
	}
	if proveRan {
		t.Fatal("the re-run must NOT re-run the expensive proof for the unchanged artifact")
	}
}
