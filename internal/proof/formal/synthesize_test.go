package formal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
)

const validDraft = `# candidate design model
# obligation: NoDoubleDispatch -> go test ./internal/x -run TestNoDoubleDispatch

action Init:
    state = "idle"

atomic action Dispatch:
    require state == "idle"
    state = "done"

always assertion NoDoubleDispatch:
    state != "double"
`

func synthStore(t *testing.T) (*contextstore.Store, string) {
	t.Helper()
	s, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	var pid string
	if err := s.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		pid, e = tx.Projects().Create(ctx, "demo", "design a worker pool", "http-service")
		return e
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return s, pid
}

func concurrentInput() SynthesisInput {
	return SynthesisInput{
		Intent:      "Build a concurrent job dispatcher with a worker pool.",
		DesignTexts: []string{"jobs are dispatched exactly once"},
		Structure: stpa.ControlStructure{
			Controllers: []string{"scheduler", "worker"},
			Actions:     []stpa.ControlAction{{ID: "CA1", Controller: "scheduler", Action: "dispatch job"}},
		},
		UCAs: []stpa.UCA{{ID: "UCA1", ControlAction: "CA1", Type: stpa.ProvidedIncorrectly, Hazard: "double dispatch"}},
	}
}

// TestSynthesizeConcurrentSpecProducesRatifiableArtifact (or-56c.2
// acceptance): a concurrent design's STPA UCAs produce a persisted,
// hash-anchored, backend-recorded artifact that a human can ratify — and the
// human signature only binds to the EXACT reviewed bytes.
func TestSynthesizeConcurrentSpecProducesRatifiableArtifact(t *testing.T) {
	ctx := context.Background()
	store, pid := synthStore(t)
	synth := func(context.Context, SynthesisInput) (string, error) { return "```\n" + validDraft + "```", nil }

	dm, err := SynthesizeDesignModel(ctx, store, pid, reliabilitytier.Standard, concurrentInput(), synth)
	if err != nil || dm == nil {
		t.Fatalf("synthesis must produce an artifact for a concurrent design: %v", err)
	}
	sum := sha256.Sum256([]byte(dm.ModelText))
	if dm.Hash != hex.EncodeToString(sum[:]) {
		t.Fatalf("the artifact must be hash-anchored to its exact bytes")
	}
	if dm.Backend != "fizzbee" {
		t.Fatalf("the backend selection must be recorded, got %q", dm.Backend)
	}
	if dm.Ratified || dm.TriggerReason == "" {
		t.Fatalf("a fresh draft is unratified and carries its trigger reason: %+v", dm)
	}
	if _, err := dm.WriteModelFile(t.TempDir()); err == nil {
		t.Fatal("an unratified draft must never enter the proof domain")
	}

	// Wrong hash refuses — the human would be vouching for unseen content.
	if _, err := RatifyDesignModel(ctx, store, pid, "deadbeef", "josebiro"); err == nil {
		t.Fatal("ratifying a mismatched hash must refuse")
	}
	ratified, err := RatifyDesignModel(ctx, store, pid, dm.Hash, "josebiro")
	if err != nil {
		t.Fatalf("ratify: %v", err)
	}
	if !ratified.Ratified || ratified.RatifiedBy != "josebiro" {
		t.Fatalf("ratification must record the signature: %+v", ratified)
	}
	if _, err := ratified.WriteModelFile(t.TempDir()); err != nil {
		t.Fatalf("a ratified model materializes for the checker: %v", err)
	}

	// Re-synthesis never clobbers the ratified artifact.
	again, err := SynthesizeDesignModel(ctx, store, pid, reliabilitytier.Standard, concurrentInput(),
		func(context.Context, SynthesisInput) (string, error) { return "OTHER", nil })
	if err != nil || again == nil || !again.Ratified || again.Hash != dm.Hash {
		t.Fatalf("re-planning must not overwrite or un-ratify the model: %+v (%v)", again, err)
	}
}

// TestSynthesizeStatelessSpecProducesNone (or-56c.2 acceptance): a stateless
// shape at standard tier is calibrated off — no artifact, nothing persisted,
// and the drafting LLM is never invoked.
func TestSynthesizeStatelessSpecProducesNone(t *testing.T) {
	ctx := context.Background()
	store, pid := synthStore(t)
	called := false
	synth := func(context.Context, SynthesisInput) (string, error) { called = true; return validDraft, nil }

	in := SynthesisInput{
		Intent:      "Store a note and return it.",
		DesignTexts: []string{"a saved note is returned verbatim"},
		Structure:   stpa.ControlStructure{Controllers: []string{"api"}},
	}
	dm, err := SynthesizeDesignModel(ctx, store, pid, reliabilitytier.Standard, in, synth)
	if err != nil || dm != nil {
		t.Fatalf("a stateless spec must produce none, got %+v (%v)", dm, err)
	}
	if called {
		t.Fatal("the drafting LLM must not be invoked when the trigger is off")
	}
	if _, ok, _ := LoadDesignModel(ctx, store, pid); ok {
		t.Fatal("nothing may persist for a stateless spec")
	}
}

// TestSynthesizeRejectsInvalidDraft: a draft that would fail the ratified-model
// rules (no invariants / unbound invariants) is refused BEFORE a human is
// asked to review it, and nothing persists.
func TestSynthesizeRejectsInvalidDraft(t *testing.T) {
	ctx := context.Background()
	store, pid := synthStore(t)
	synth := func(context.Context, SynthesisInput) (string, error) { return "action Init:\n    x = 1\n", nil }

	_, err := SynthesizeDesignModel(ctx, store, pid, reliabilitytier.Standard, concurrentInput(), synth)
	if err == nil {
		t.Fatal("an invariant-free draft must be rejected")
	}
	if !strings.Contains(err.Error(), "no invariants") && !strings.Contains(err.Error(), "draft rejected") {
		t.Fatalf("the rejection must say why: %v", err)
	}
	if _, ok, _ := LoadDesignModel(ctx, store, pid); ok {
		t.Fatal("a rejected draft must not persist")
	}

	// A synthesizer error propagates (fail-open is the CALLER's posture).
	_, err = SynthesizeDesignModel(ctx, store, pid, reliabilitytier.Standard, concurrentInput(),
		func(context.Context, SynthesisInput) (string, error) { return "", errors.New("brain offline") })
	if err == nil {
		t.Fatal("a synthesis error must surface to the caller")
	}
}
