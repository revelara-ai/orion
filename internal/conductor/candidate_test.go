package conductor

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// TestProposeCandidateOnAccept (or-ykz.8): a passing run yields >=1 self-evolution candidate
// (active=false, provenance=generation) that is excluded from active recall; a non-Accept run
// yields none. The generation trust tier keeps it out of any proof prompt (covered by
// TestProofDomainExcludesGenerationMemory).
func TestProposeCandidateOnAccept(t *testing.T) {
	ctx := context.Background()
	mem, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mem.Close() }()

	accept := proof.Report{Outcome: truthalign.Outcome{
		Verdict: truthalign.Accept,
		Modes:   []truthalign.ModeResult{{Mode: "behavioral", Pass: true}},
	}}
	if err := proposeCandidate(ctx, mem, "T1", accept); err != nil {
		t.Fatal(err)
	}
	cands, err := mem.ListCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Fatalf("a passing run should yield exactly 1 candidate; got %d", len(cands))
	}
	if c := cands[0]; !c.Candidate || c.TrustTier != memory.TrustGeneration {
		t.Fatalf("candidate must be active=false + generation; got Candidate=%v tier=%s", c.Candidate, c.TrustTier)
	}
	if got, _ := mem.Retrieve(ctx, "", memory.LTM); len(got) != 0 {
		t.Fatalf("the candidate must be excluded from active recall; got %d", len(got))
	}

	// A non-Accept run yields no candidate.
	mem2, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mem2.Close() }()
	reject := proof.Report{Outcome: truthalign.Outcome{Verdict: truthalign.Reject}}
	if err := proposeCandidate(ctx, mem2, "T2", reject); err != nil {
		t.Fatal(err)
	}
	if cands, _ := mem2.ListCandidates(ctx); len(cands) != 0 {
		t.Fatalf("a non-Accept run should yield no candidate; got %d", len(cands))
	}
}
