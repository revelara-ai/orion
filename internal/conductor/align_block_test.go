package conductor

import (
	"context"
	"sync/atomic"
	"testing"

	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// assertOpenEscalationContains fails unless an open inbox escalation's reason
// contains sub.
func assertOpenEscalationContains(t *testing.T, oc *orchestrator.Conductor, ctx context.Context, sub string) {
	t.Helper()
	found := false
	if err := oc.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		open, e := tx.Escalations().ListOpen(ctx)
		if e != nil {
			return e
		}
		for _, esc := range open {
			if strings.Contains(esc.Reason, sub) || strings.Contains(esc.Detail, sub) {
				found = true
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("expected an open escalation whose reason contains %q", sub)
	}
}

// TestAlignGateBlocksCorroboratedHigh (or-809): with ORION_ALIGN_GATE=block, a
// proof-passing but corroborated-HIGH-misaligned module must NOT close — the
// green light is removed (Accept→Inconclusive) and it does not deliver.
func TestAlignGateBlocksCorroboratedHigh(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a service + proof")
	}
	t.Setenv("ORION_ALIGN_GATE", "block")
	oc, ctx := ratifiedTimeService(t)

	// Deterministic mock judge: always high+misaligned, so the second corroboration
	// pass agrees and the block stands.
	highMisaligned := func(context.Context, string, string, []spec.BehavioralCase) (AlignVerdict, error) {
		return AlignVerdict{Aligned: false, Severity: "high", Concern: "hardcoded, not the real intent"}, nil
	}
	res, err := BuildAndProve(ctx, oc.Store(), nil, highMisaligned, nil, "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.Verdict == string(truthalign.Accept) || res.Closed {
		t.Fatalf("a corroborated-high misalignment must remove the green light, got %+v", res)
	}
	// The block must reach the operator: an escalation carrying the alignment
	// concern is in the inbox (a regression that blocks the verdict but drops the
	// escalation must fail here).
	assertOpenEscalationContains(t, oc, ctx, "alignment(high)")
}

// TestAlignGateDowngradesUncorroboratedHigh (or-809 G5): a high verdict the
// second pass does NOT corroborate is downgraded to medium — the module SHIPS
// (proof is the right-to-ship) but a surface-to-human align-review is filed.
func TestAlignGateDowngradesUncorroboratedHigh(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a service + proof")
	}
	t.Setenv("ORION_ALIGN_GATE", "block")
	oc, ctx := ratifiedTimeService(t)

	var calls int32
	flaky := func(context.Context, string, string, []spec.BehavioralCase) (AlignVerdict, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return AlignVerdict{Aligned: false, Severity: "high", Concern: "maybe drift"}, nil
		}
		return AlignVerdict{Aligned: true, Severity: "none"}, nil // second pass disagrees
	}
	res, err := BuildAndProve(ctx, oc.Store(), nil, flaky, nil, "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.Verdict != string(truthalign.Accept) || !res.Closed {
		t.Fatalf("an uncorroborated high must downgrade to medium and SHIP, got %+v", res)
	}
	// A medium align-review escalation must be in the inbox (surface-to-human).
	assertOpenEscalationContains(t, oc, ctx, "alignment review")
}

// TestAlignGateNeverCalledOnReject (or-809 G1): the aligner is never consulted
// when proof did not Accept — a Reject→Accept path is structurally unreachable.
func TestAlignGateNeverCalledOnReject(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a service + proof")
	}
	t.Setenv("ORION_ALIGN_GATE", "block")
	oc, ctx := ratifiedTimeService(t)

	var calls int32
	spy := func(context.Context, string, string, []spec.BehavioralCase) (AlignVerdict, error) {
		atomic.AddInt32(&calls, 1)
		return AlignVerdict{Aligned: true, Severity: "none"}, nil
	}
	brokenGen := func(_ context.Context, gs sandbox.GenSpec, dir, _ string) (sandbox.GeneratedArtifact, error) {
		return writeBrokenTimeService(dir, gs)
	}
	res, err := BuildAndProve(ctx, oc.Store(), brokenGen, spy, nil, "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.Verdict == string(truthalign.Accept) {
		t.Fatalf("the broken service must not Accept: %+v", res)
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Fatalf("the aligner must NEVER be called on a non-Accept proof (G1), got %d calls", n)
	}
}
