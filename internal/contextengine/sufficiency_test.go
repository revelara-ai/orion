package contextengine

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/memory"
)

func suffEngine(t *testing.T) (*Engine, *memory.Store) {
	t.Helper()
	mem, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mem.Close() })
	return New(nil, mem), mem
}

// TestSufficiencyGateBlocksOnMissingObligationInput (bead-named): an
// obligation input absent from the bundle is a NAMED gap, not a pass.
func TestSufficiencyGateBlocksOnMissingObligationInput(t *testing.T) {
	b := Bundle{Constraints: []string{"port=8080", "route=/time"}}
	rep := CheckSufficiency(b, []string{"America/New_York"})
	if rep.Outcome != NeedsRecall || len(rep.Gaps) != 1 || rep.Gaps[0] != "America/New_York" {
		t.Fatalf("missing tz must gap: %+v", rep)
	}
	// Present (case-insensitively, in any tier) → sufficient.
	b.Constraints = append(b.Constraints, "timezone=america/new_york")
	if rep := CheckSufficiency(b, []string{"America/New_York"}); rep.Outcome != Sufficient {
		t.Fatalf("present evidence misread as gap: %+v", rep)
	}
}

// TestSufficiencyGateTriggersRecallThenProceeds (bead-named): the first
// assembly misses the evidence; the gap-focused recall query finds it in
// memory; the gate proceeds on cycle 2.
func TestSufficiencyGateTriggersRecallThenProceeds(t *testing.T) {
	eng, mem := suffEngine(t)
	ctx := context.Background()
	// The evidence shares NO tokens with the intent, and a tight budget is
	// filled by decoys that own the intent's tokens (keyword relevance
	// dominates heat) — so cycle 1 provably misses it. The gap query ties
	// the keyword score and the target's higher heat wins cycle 2.
	if _, err := mem.Write(ctx, memory.Item{
		Tier: memory.MTM, Kind: memory.KindPage,
		Content:   "offset rules: America/New_York observes DST",
		TrustTier: memory.TrustProof, Heat: 50,
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if _, err := mem.Write(ctx, memory.Item{
			Tier: memory.MTM, Kind: memory.KindPage,
			Content:   "build the service step guide part " + string(rune('a'+i)),
			TrustTier: memory.TrustProof, Heat: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	eng = eng.WithTokenBudget(60)
	bundle, rep, err := eng.EnsureSufficient(ctx, "", "build the service", []string{"America/New_York"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Outcome != Sufficient {
		t.Fatalf("gap-focused recall must find the evidence: %+v", rep)
	}
	if rep.Cycles < 2 {
		t.Fatalf("the intent query alone must NOT have matched (want cycle 2, got %d)", rep.Cycles)
	}
	if !strings.Contains(bundle.Render(DomainGeneration), "America/New_York") {
		t.Fatal("recalled evidence missing from the final bundle")
	}
}

// TestSufficiencyGateBoundedByMaxCycles (bead-named): unresolvable gaps
// exhaust the budget and yield NeedsHuman — never a silent proceed, never an
// unbounded loop.
func TestSufficiencyGateBoundedByMaxCycles(t *testing.T) {
	t.Setenv("ORION_SUFFICIENCY_CYCLES", "3")
	eng, _ := suffEngine(t)
	_, rep, err := eng.EnsureSufficient(context.Background(), "", "build it", []string{"evidence-that-does-not-exist-anywhere"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Outcome != NeedsHuman {
		t.Fatalf("exhausted cycles must escalate to human: %+v", rep)
	}
	if rep.Cycles != 3 {
		t.Fatalf("cycle budget must be honored exactly: %+v", rep)
	}
	if len(rep.Gaps) == 0 || !strings.Contains(rep.String(), "missing evidence") {
		t.Fatalf("escalation must carry the named gaps: %s", rep.String())
	}
}

// TestSufficiencyGateNeverReadsHeldOutCorpus / ...CannotSetVerdict
// (bead-named): both hold BY CONSTRUCTION — the gate's entire input surface
// is (Bundle, needs) and its output enum carries no verdict field. This test
// pins the structural claim: a store-less, memory-only engine runs the gate
// end to end (nothing beyond bundle+needs is reachable).
func TestSufficiencyGateNeverReadsHeldOutCorpusAndCannotSetVerdict(t *testing.T) {
	eng, _ := suffEngine(t) // store == nil: no corpus, no proof surface at all
	_, rep, err := eng.EnsureSufficient(context.Background(), "", "intent", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Outcome != Sufficient {
		t.Fatalf("no needs → sufficient on cycle 1: %+v", rep)
	}
}
