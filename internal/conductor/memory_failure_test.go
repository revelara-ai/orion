package conductor

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// TestRejectWritesFailureFactsAndQuarantinedNarrative (or-hd3.5): a non-Accept verdict
// writes the proof-derived failure FACT as trusted (proof-tier) and a supplied agent
// NARRATIVE as quarantined (generation-tier). The trust tiers are what the context engine
// partitions on (proof → trusted block, generation → untrusted block; proof prompts exclude
// generation entirely — covered by the contextengine partition tests), so asserting the
// tiers here is asserting the trusted/untrusted split of the next attempt's bundle.
func TestRejectWritesFailureFactsAndQuarantinedNarrative(t *testing.T) {
	ctx := context.Background()
	mem, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mem.Close() }()

	rejectReport := proof.Report{Outcome: truthalign.Outcome{
		Verdict:    truthalign.Reject,
		Dissenting: []string{"behavioral"},
		Modes: []truthalign.ModeResult{
			{Mode: "behavioral", Pass: false, Metrics: map[string]float64{"pass_rate": 0.5}},
			{Mode: "empirical", Pass: true},
		},
	}}
	if err := rememberFailure(ctx, mem, "T1", rejectReport, "the regex was too greedy; try anchoring", "Proof verdict: Reject.\nFAILED case case-abc123: want 200 got 500"); err != nil {
		t.Fatal(err)
	}

	items, err := mem.Retrieve(ctx, "", memory.MTM)
	if err != nil {
		t.Fatal(err)
	}
	var proofContents []string
	var narrative *memory.Item
	for i := range items {
		if items[i].Kind != memory.KindFailure {
			continue
		}
		switch items[i].TrustTier {
		case memory.TrustProof:
			proofContents = append(proofContents, items[i].Content)
		case memory.TrustGeneration:
			narrative = &items[i]
		}
	}
	joined := strings.Join(proofContents, "\n")
	if len(proofContents) < 2 {
		t.Fatalf("Reject must write the proof-tier failure FACT and the causal analysis (or-gb1.3), got %d items", len(proofContents))
	}
	if !strings.Contains(joined, "FAILED") || !strings.Contains(joined, "behavioral") {
		t.Fatalf("failure fact should carry the verdict + dissenting mode; got %q", joined)
	}
	if !strings.Contains(joined, "case-abc123") {
		t.Fatalf("the causal analysis must persist a failing case id (or-gb1.3); got %q", joined)
	}
	if narrative == nil {
		t.Fatal("a supplied agent narrative must be written as a quarantined generation-tier item")
	}
	if !strings.Contains(narrative.Content, "regex") {
		t.Fatalf("narrative should carry the agent self-report; got %q", narrative.Content)
	}

	// Accept must write neither failure item (that path is rememberOutcome's).
	acceptReport := proof.Report{Outcome: truthalign.Outcome{Verdict: truthalign.Accept}}
	mem2, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mem2.Close() }()
	if err := rememberFailure(ctx, mem2, "T2", acceptReport, "should be ignored", ""); err != nil {
		t.Fatal(err)
	}
	got2, err := mem2.Retrieve(ctx, "", memory.MTM)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range got2 {
		if it.Kind == memory.KindFailure {
			t.Fatal("Accept must not write a failure item")
		}
	}
}

// TestRejectWithoutNarrativeWritesOnlyTheFact (or-hd3.5): when no agent narrative is
// supplied (the current production wiring), only the trusted proof fact is written — never an
// empty quarantined item.
func TestRejectWithoutNarrativeWritesOnlyTheFact(t *testing.T) {
	ctx := context.Background()
	mem, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mem.Close() }()
	r := proof.Report{Outcome: truthalign.Outcome{Verdict: truthalign.Inconclusive, Dissenting: []string{"hazard"}}}
	if err := rememberFailure(ctx, mem, "T3", r, "   ", ""); err != nil {
		t.Fatal(err)
	}
	items, err := mem.Retrieve(ctx, "", memory.MTM)
	if err != nil {
		t.Fatal(err)
	}
	failures := 0
	for _, it := range items {
		if it.Kind == memory.KindFailure {
			failures++
			if it.TrustTier != memory.TrustProof {
				t.Fatalf("the only failure item must be the trusted fact; got tier %s", it.TrustTier)
			}
		}
	}
	if failures != 1 {
		t.Fatalf("blank narrative must yield exactly one (proof-tier) failure item; got %d", failures)
	}
}
