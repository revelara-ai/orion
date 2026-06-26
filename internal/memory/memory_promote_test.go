package memory

import (
	"context"
	"testing"
)

// TestPromotionPromotesHotItem (or-hd3.6): a hot, frequently-retrieved MTM item is promoted
// to LTM; a cold/rarely-used item is not.
func TestPromotionPromotesHotItem(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	hotID, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "alpha hot pattern", TrustTier: TrustProof, Heat: 1})
	if err != nil {
		t.Fatal(err)
	}
	coldID, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "beta cold pattern", TrustTier: TrustProof, Heat: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ { // make alpha both hot and frequently used
		if _, err := s.Retrieve(ctx, "alpha", MTM); err != nil {
			t.Fatal(err)
		}
	}
	pid, n, err := s.Promote(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || pid == "" {
		t.Fatalf("expected exactly 1 promotion with an id; got n=%d id=%q", n, pid)
	}
	ltm, err := s.Retrieve(ctx, "", LTM)
	if err != nil {
		t.Fatal(err)
	}
	if len(ltm) != 1 || ltm[0].ID != hotID {
		t.Fatalf("hot item should be promoted to LTM; got first=%s (n=%d)", firstID(ltm), len(ltm))
	}
	mtm, err := s.Retrieve(ctx, "", MTM)
	if err != nil {
		t.Fatal(err)
	}
	if len(mtm) != 1 || mtm[0].ID != coldID {
		t.Fatalf("cold item should remain in MTM; got first=%s (n=%d)", firstID(mtm), len(mtm))
	}
}

// TestPromotionPreservesTrustTier (or-hd3.6): a promoted generation-tier item stays
// generation (quarantined) — promotion must never launder trust.
func TestPromotionPreservesTrustTier(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	genID, err := s.Write(ctx, Item{Tier: MTM, Kind: KindFailure, Content: "alpha gen narrative", TrustTier: TrustGeneration, Heat: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Retrieve(ctx, "alpha", MTM); err != nil {
			t.Fatal(err)
		}
	}
	if _, n, err := s.Promote(ctx); err != nil || n != 1 {
		t.Fatalf("promote: n=%d err=%v", n, err)
	}
	ltm, err := s.Retrieve(ctx, "", LTM)
	if err != nil {
		t.Fatal(err)
	}
	if len(ltm) != 1 || ltm[0].ID != genID {
		t.Fatalf("promoted item missing from LTM; got n=%d", len(ltm))
	}
	if ltm[0].TrustTier != TrustGeneration {
		t.Fatalf("promotion must preserve the trust tier; want generation, got %s", ltm[0].TrustTier)
	}
}

// TestPromotionReversible (or-hd3.6): a promotion can be fully undone by its promotionID.
func TestPromotionReversible(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	id, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "alpha reversible", Heat: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Retrieve(ctx, "alpha", MTM); err != nil {
			t.Fatal(err)
		}
	}
	pid, n, err := s.Promote(ctx)
	if err != nil || n != 1 {
		t.Fatalf("promote: n=%d err=%v", n, err)
	}
	if ltm, _ := s.Retrieve(ctx, "", LTM); len(ltm) != 1 {
		t.Fatalf("expected item in LTM after promote")
	}
	if err := s.ReversePromotion(ctx, pid); err != nil {
		t.Fatal(err)
	}
	ltm, err := s.Retrieve(ctx, "", LTM)
	if err != nil {
		t.Fatal(err)
	}
	if len(ltm) != 0 {
		t.Fatalf("reversal should empty LTM; got %d", len(ltm))
	}
	mtm, err := s.Retrieve(ctx, "", MTM)
	if err != nil {
		t.Fatal(err)
	}
	if len(mtm) != 1 || mtm[0].ID != id {
		t.Fatalf("reversal should restore the item to MTM; got first=%s (n=%d)", firstID(mtm), len(mtm))
	}

	// The promotion tag must have been CLEARED on reversal: a re-promotion gets a fresh
	// batch id, and replaying the STALE id must not reverse the re-promoted item.
	pid2, n2, err := s.Promote(ctx)
	if err != nil || n2 != 1 {
		t.Fatalf("re-promote after reversal: n=%d err=%v", n2, err)
	}
	if pid2 == pid {
		t.Fatalf("re-promotion should yield a fresh batch id, not the stale %q", pid)
	}
	if err := s.ReversePromotion(ctx, pid); err != nil { // stale id → must be a no-op
		t.Fatal(err)
	}
	if ltm, _ := s.Retrieve(ctx, "", LTM); len(ltm) != 1 || ltm[0].ID != id {
		t.Fatal("a stale promotion id must not reverse the re-promoted item (tag was not cleared)")
	}
}

// TestPinExistingItem (or-hd3.6 note): Store.Pin post-hoc pins an already-written item, and a
// pinned item survives eviction pressure (anti-erosion).
func TestPinExistingItem(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	id, err := s.Write(ctx, Item{Tier: MTM, Kind: KindDecision, Content: "later-critical decision", Heat: 0})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Retrieve(ctx, "", MTM); len(got) != 1 || got[0].Pinned {
		t.Fatal("item should start unpinned")
	}
	if err := s.Pin(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, err := s.Retrieve(ctx, "", MTM)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Pinned {
		t.Fatalf("Pin should mark the existing item pinned; got %+v", got)
	}
	if err := s.EvictToCapacity(ctx, MTM, 0); err != nil {
		t.Fatal(err)
	}
	if rem, _ := s.Retrieve(ctx, "", MTM); len(rem) != 1 || rem[0].ID != id {
		t.Fatal("pinned item must survive eviction pressure")
	}
}
