package memory

import (
	"context"
	"testing"
)

// TestCandidateExcludedFromRecall (or-ykz.8): a candidate (active=false) item is excluded from
// active recall (Retrieve) but is enumerable via ListCandidates for the self-evolution lifecycle.
func TestCandidateExcludedFromRecall(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	active, err := s.Write(ctx, Item{Tier: LTM, Kind: KindPattern, Content: "active pattern", TrustTier: TrustProof})
	if err != nil {
		t.Fatal(err)
	}
	cand, err := s.Write(ctx, Item{Tier: LTM, Kind: KindProcedure, Content: "candidate proc", TrustTier: TrustGeneration, Candidate: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Retrieve(ctx, "", LTM)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != active {
		t.Fatalf("active recall must exclude candidates; got first=%s (n=%d)", firstID(got), len(got))
	}
	cands, err := s.ListCandidates(ctx, LTM)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].ID != cand || !cands[0].Candidate || cands[0].TrustTier != TrustGeneration {
		t.Fatalf("ListCandidates should return the generation candidate; got %+v", cands)
	}
}
