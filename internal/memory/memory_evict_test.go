package memory

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestSummarizeThenEvictNoLossOnCrash (or-hd3.4): a crash between the summary-write (phase
// 1) and the raw-drop (phase 2) must leave every raw page intact. We simulate the crash by
// running phase 1 only (summarizeForEviction) and asserting the raws are all still present
// and the summaries are durable — so no content is ever lost at the crash point.
func TestSummarizeThenEvictNoLossOnCrash(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	const n = 4
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPage, Content: fmt.Sprintf("page %d unique", i), Heat: float64(i)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	// Phase 1 only — simulates a crash AFTER summaries are durable but BEFORE the raw drop.
	dropIDs, err := s.summarizeForEviction(ctx, MTM, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(dropIDs) != n-1 {
		t.Fatalf("expected %d raws queued to drop, got %d", n-1, len(dropIDs))
	}
	got, err := s.Retrieve(ctx, "", MTM)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	summaries := 0
	for _, it := range got {
		present[it.ID] = true
		if it.Kind == KindSummary {
			summaries++
		}
	}
	for _, id := range ids {
		if !present[id] {
			t.Fatalf("raw %s was dropped before its summary was durable — data loss on crash", id)
		}
	}
	if summaries != n-1 {
		t.Fatalf("phase 1 should have written %d summaries, got %d", n-1, summaries)
	}
}

// TestSecurityItemNeverSummarizedAway (or-hd3.4): a security_relevant item is retained in
// FULL under eviction pressure — never summarized or dropped — even when it is the coldest
// item in the tier.
func TestSecurityItemNeverSummarizedAway(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	// A cold security-relevant item that WOULD be first to go if it were a candidate.
	secID, err := s.Write(ctx, Item{Tier: MTM, Kind: KindDecision, Content: "API key rotation policy", SecurityRelevant: true, Heat: 0})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPage, Content: fmt.Sprintf("normal %d", i), Heat: float64(i + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.EvictToCapacity(ctx, MTM, 1); err != nil {
		t.Fatal(err)
	}
	got, err := s.Retrieve(ctx, "", MTM)
	if err != nil {
		t.Fatal(err)
	}
	var sec *Item
	normalSummaries := 0
	for i := range got {
		if got[i].ID == secID {
			sec = &got[i]
		}
		if got[i].Kind == KindSummary {
			normalSummaries++
		}
	}
	if sec == nil {
		t.Fatal("security_relevant item was evicted — it must be retained in full")
	}
	if sec.Kind != KindDecision || sec.Content != "API key rotation policy" {
		t.Fatalf("security item must be retained in FULL, not summarized; got kind=%s content=%q", sec.Kind, sec.Content)
	}
	if normalSummaries == 0 {
		t.Fatal("eviction should have summarized the cold normal pages (sanity: eviction actually ran)")
	}
}

// TestEvictionDropsColdSummariesWithoutCascade (or-hd3.4 review F1/F5/F6/F7): under
// sustained pressure, summaries must NOT be re-summarized (which would nest markers and
// erode the original content); cold summaries age out directly, and the tier count stays
// bounded.
func TestEvictionDropsColdSummariesWithoutCascade(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	anchor, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPage, Content: "anchor stays hot", Heat: 100})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPage, Content: fmt.Sprintf("cold page %d ENDTAIL", i), Heat: 1}); err != nil {
			t.Fatal(err)
		}
	}
	for round := 0; round < 4; round++ {
		if err := s.EvictToCapacity(ctx, MTM, 1); err != nil {
			t.Fatalf("evict round %d: %v", round, err)
		}
	}
	got, err := s.Retrieve(ctx, "", MTM)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range got {
		if strings.Count(it.Content, "[summary ") > 1 {
			t.Fatalf("summary was re-summarized (cascade/nesting): %q", it.Content)
		}
	}
	if len(got) > 2 {
		t.Fatalf("repeated eviction must bound the tier (cold summaries dropped); got %d items", len(got))
	}
	foundAnchor := false
	for _, it := range got {
		if it.ID == anchor {
			foundAnchor = true
		}
	}
	if !foundAnchor {
		t.Fatal("the hot anchor must survive repeated eviction")
	}
}

// TestOpenMigratesPartialDB (or-hd3.4 review): the hd3.3 -> hd3.4 upgrade path — a DB that
// already has visit_count (hd3.3) but not security_relevant (hd3.4) gets only the missing
// column added, and security_relevant then round-trips.
func TestOpenMigratesPartialDB(t *testing.T) {
	dir := t.TempDir()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE memory_items (
		id TEXT PRIMARY KEY, tier TEXT NOT NULL, kind TEXT NOT NULL, content TEXT NOT NULL,
		content_hash TEXT NOT NULL, pinned INTEGER NOT NULL DEFAULT 0,
		trust_tier TEXT NOT NULL DEFAULT 'generation', heat REAL NOT NULL DEFAULT 0,
		visit_count INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL, last_accessed_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open/migrate partial DB: %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	if _, err := s.Write(ctx, Item{Tier: MTM, Kind: KindDecision, Content: "rotate keys", SecurityRelevant: true}); err != nil {
		t.Fatalf("write after partial migrate (security_relevant missing?): %v", err)
	}
	got, err := s.Retrieve(ctx, "", MTM)
	if err != nil {
		t.Fatalf("retrieve after partial migrate: %v", err)
	}
	if len(got) != 1 || !got[0].SecurityRelevant {
		t.Fatalf("security_relevant should round-trip after migration; got %+v", got)
	}
}
