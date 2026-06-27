package memory

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestEffectiveHeatRecencyAndFrequency(t *testing.T) {
	now := time.Now().UTC()
	recent := now.Add(-time.Hour)
	old := now.Add(-30 * 24 * time.Hour)
	if effectiveHeat(1, recent, 0, now) <= effectiveHeat(1, old, 0, now) {
		t.Fatal("recent access should outrank old (recency decay)")
	}
	if effectiveHeat(1, recent, 10, now) <= effectiveHeat(1, recent, 0, now) {
		t.Fatal("more visits should outrank fewer (frequency boost)")
	}
}

// TestHeatRetrieveBumpsAndEvictionKeepsHotter: retrieving an item as relevant bumps its
// visit_count + recency, raising its effective heat so it ranks first and survives
// eviction over an equally-base-weighted but unretrieved sibling.
func TestHeatRetrieveBumpsAndEvictionKeepsHotter(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	idA, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "alpha topic", TrustTier: TrustProof, Heat: 1})
	if err != nil {
		t.Fatal(err)
	}
	idB, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "beta topic", TrustTier: TrustProof, Heat: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.RecordAccess(ctx, idA); err != nil {
			t.Fatal(err)
		}
	}
	ranked, err := s.Retrieve(ctx, "", MTM)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) != 2 || ranked[0].ID != idA {
		t.Fatalf("frequently-retrieved A should rank first; got %d items, first=%s", len(ranked), firstID(ranked))
	}
	if ranked[0].VisitCount < 3 {
		t.Fatalf("A visit_count should be >=3 after 3 matched retrievals, got %d", ranked[0].VisitCount)
	}
	if err := s.EvictToCapacity(ctx, MTM, 1); err != nil {
		t.Fatal(err)
	}
	rem, err := s.Retrieve(ctx, "", MTM)
	if err != nil {
		t.Fatal(err)
	}
	// Hotter A survives in full; colder B is summarized away (raw dropped, summary kept).
	var haveA, haveBraw, haveSummary bool
	for _, it := range rem {
		switch {
		case it.ID == idA:
			haveA = true
		case it.ID == idB:
			haveBraw = true
		case it.Kind == KindSummary:
			haveSummary = true
		}
	}
	if !haveA {
		t.Fatalf("hotter A should survive eviction in full; remaining n=%d", len(rem))
	}
	if haveBraw {
		t.Fatal("colder B's raw page should be evicted")
	}
	if !haveSummary {
		t.Fatal("evicted B should leave a summary (no hard-drop)")
	}
}

func firstID(items []Item) string {
	if len(items) == 0 {
		return "(none)"
	}
	return items[0].ID
}

// TestRetrieveReturnsBumpedValuesSameCall (or-hd3.3 review F1): the Item values returned
// by the SAME matching Retrieve that records the access must reflect the bump, not the
// stale pre-bump state.
// TestRecordAccessBumpsVisitCount (or-vx8): Retrieve is read-only; RecordAccess is what bumps
// visit_count + last_accessed (the caller-controlled heat feedback).
func TestRecordAccessBumpsVisitCount(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	id, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "alpha topic", Heat: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Retrieve must NOT bump.
	if got, _ := s.Retrieve(ctx, "alpha", MTM); len(got) != 1 || got[0].VisitCount != 0 {
		t.Fatalf("Retrieve must be read-only (no bump); got VisitCount=%d", got[0].VisitCount)
	}
	if err := s.RecordAccess(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, err := s.Retrieve(ctx, "", MTM)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].VisitCount != 1 {
		t.Fatalf("RecordAccess should bump visit_count to 1; got %d", got[0].VisitCount)
	}
	if got[0].LastAccessed.IsZero() {
		t.Fatal("RecordAccess should set last_accessed")
	}
}

// TestRankRelevanceBeatsHugeBaseHeat (or-hd3.3 review F2): a query-matched item outranks a
// far hotter non-match — the tiered comparator holds at any heat magnitude.
func TestRankRelevanceBeatsHugeBaseHeat(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	idNeedle, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "the needle", Heat: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPattern, Content: "a haystack", Heat: 5000}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Retrieve(ctx, "needle", MTM)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != idNeedle {
		t.Fatalf("matched item must outrank a far hotter non-match; first=%s", firstID(got))
	}
}

// TestBumpSkipsPinnedItems (or-hd3.3 review F8): pinned anti-erosion items are never
// mutated by the retrieve bump.
// TestRecordAccessSkipsPinned (or-vx8): RecordAccess never bumps a pinned anti-erosion item.
func TestRecordAccessSkipsPinned(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	id, err := s.Write(ctx, Item{Tier: LTM, Kind: KindSpec, Content: "alpha spec", Heat: 1, Pinned: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.RecordAccess(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Retrieve(ctx, "", LTM)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("want pinned item %s, got first=%s (n=%d)", id, firstID(got), len(got))
	}
	if got[0].VisitCount != 0 {
		t.Fatalf("pinned item must not be bumped; VisitCount=%d", got[0].VisitCount)
	}
}

// TestOpenMigratesOldDBMissingVisitCount (or-hd3.3 review F5/F6): opening a pre-heat-model
// DB (no visit_count column) adds the column; opening a fresh DB is a clean no-op.
func TestOpenMigratesOldDBMissingVisitCount(t *testing.T) {
	dir := t.TempDir()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	// Pre-heat-model schema: memory_items WITHOUT visit_count.
	if _, err := raw.Exec(`CREATE TABLE memory_items (
		id TEXT PRIMARY KEY, tier TEXT NOT NULL, kind TEXT NOT NULL, content TEXT NOT NULL,
		content_hash TEXT NOT NULL, pinned INTEGER NOT NULL DEFAULT 0,
		trust_tier TEXT NOT NULL DEFAULT 'generation', heat REAL NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL, last_accessed_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open/migrate old DB: %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	if _, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPage, Content: "x"}); err != nil {
		t.Fatalf("write after migrate (visit_count missing?): %v", err)
	}
	if _, err := s.Retrieve(ctx, "x", MTM); err != nil {
		t.Fatalf("retrieve after migrate: %v", err)
	}
}
