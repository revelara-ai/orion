package memory

import (
	"context"
	"testing"
)

// TestSummaryInheritsCreatedAt (or-wq5): a summary written during summarize-evict inherits the
// source raw's created_at (original first-written time), not "now" — provenance is preserved.
func TestSummaryInheritsCreatedAt(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)

	// A hot anchor survives; a cold raw is summarized-then-dropped.
	if _, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPage, Content: "hot anchor", Heat: 100}); err != nil {
		t.Fatal(err)
	}
	coldID, err := s.Write(ctx, Item{Tier: MTM, Kind: KindPage, Content: "cold page to summarize", Heat: 1})
	if err != nil {
		t.Fatal(err)
	}
	var rawCreated string
	if err := s.db.QueryRowContext(ctx, `SELECT created_at FROM memory_items WHERE id=?`, coldID).Scan(&rawCreated); err != nil {
		t.Fatal(err)
	}

	if err := s.EvictToCapacity(ctx, MTM, 1); err != nil {
		t.Fatal(err)
	}

	var sumCreated string
	if err := s.db.QueryRowContext(ctx, `SELECT created_at FROM memory_items WHERE kind=? LIMIT 1`, KindSummary).Scan(&sumCreated); err != nil {
		t.Fatalf("no summary was written: %v", err)
	}
	if sumCreated != rawCreated {
		t.Fatalf("summary created_at = %q; should inherit the source raw's %q (not reset to now)", sumCreated, rawCreated)
	}
}
