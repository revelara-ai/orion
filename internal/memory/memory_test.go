package memory

import (
	"context"
	"fmt"
	"testing"
)

func openMem(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open memory: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestPinnedSpecItemNeverEvicted: a pinned spec constraint survives heavy
// eviction pressure (categorical pin, not heat-based).
func TestPinnedSpecItemNeverEvicted(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()

	if _, err := s.Write(ctx, Item{
		Tier: MTM, Kind: KindSpec, Content: "MUST return the current time in UTC",
		Pinned: true, TrustTier: TrustHuman, Heat: 0, // zero heat — would be first evicted if not pinned
	}); err != nil {
		t.Fatalf("write pinned: %v", err)
	}
	// Flood with hotter, unpinned pages.
	for i := 0; i < 200; i++ {
		if _, err := s.Write(ctx, Item{
			Tier: MTM, Kind: KindPage, Content: fmt.Sprintf("noise page %d", i),
			TrustTier: TrustGeneration, Heat: float64(i + 1),
		}); err != nil {
			t.Fatalf("write noise: %v", err)
		}
	}

	// Severe pressure: keep only 5 non-pinned items.
	if err := s.EvictToCapacity(ctx, MTM, 5); err != nil {
		t.Fatalf("evict: %v", err)
	}

	got, err := s.Retrieve(ctx, "UTC", MTM)
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	foundPinned := false
	nonPinned := 0
	for _, it := range got {
		if it.Pinned && it.Kind == KindSpec {
			foundPinned = true
		}
		if !it.Pinned {
			nonPinned++
		}
	}
	if !foundPinned {
		t.Fatal("pinned spec item was evicted under pressure")
	}
	if nonPinned > 5 {
		t.Fatalf("eviction kept %d non-pinned items, want <= 5", nonPinned)
	}
}

// TestRetrieveRanksPinnedAndRelevantFirst.
func TestRetrieveRanksPinnedThenRelevant(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	_, _ = s.Write(ctx, Item{Tier: MTM, Kind: KindPage, Content: "irrelevant", Heat: 100})
	_, _ = s.Write(ctx, Item{Tier: MTM, Kind: KindPage, Content: "talks about timezone UTC", Heat: 1})
	_, _ = s.Write(ctx, Item{Tier: MTM, Kind: KindSpec, Content: "pinned constraint", Pinned: true, Heat: 0})

	got, err := s.Retrieve(ctx, "UTC", MTM)
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(got) < 2 || !got[0].Pinned {
		t.Fatalf("pinned item should rank first; got %+v", got)
	}
	if got[1].Content != "talks about timezone UTC" {
		t.Fatalf("relevant item should rank above irrelevant; got %q", got[1].Content)
	}
}

// TestPersistsAcrossReopen: MTM/LTM items survive a store reopen (separate DB).
func TestPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s1.Write(ctx, Item{Tier: LTM, Kind: KindPattern, Content: "retry with backoff", TrustTier: TrustProof}); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = s1.Close()

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s2.Close() }()
	got, err := s2.Retrieve(ctx, "", LTM)
	if err != nil || len(got) != 1 {
		t.Fatalf("expected 1 LTM item after reopen, got %d err=%v", len(got), err)
	}
}
