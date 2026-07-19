package memory

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// seedPromotable writes n MTM items that qualify for promotion (hot + visited).
func seedPromotable(t *testing.T, s *Store, n int) []string {
	t.Helper()
	ctx := context.Background()
	var ids []string
	for i := 0; i < n; i++ {
		id, err := s.Write(ctx, Item{
			Tier: MTM, Kind: KindPattern, TrustTier: TrustProof, Heat: 3.0,
			Content: fmt.Sprintf("promotable pattern %d", i),
		})
		if err != nil {
			t.Fatal(err)
		}
		// Qualify: promoteMinVisits retrievals-as-relevant (direct column bump —
		// same-package test; the heat model itself is covered elsewhere).
		if _, err := s.db.ExecContext(ctx, `UPDATE memory_items SET visit_count=?, heat=? WHERE id=?`, promoteMinVisits, 3.0, id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// TestLTMPromotionCrashSafeNoCorruption (or-mija, North-Star predicate): a
// promotion interrupted MID-BATCH (fault injected between per-item updates
// inside the transaction) must be ALL-OR-NOTHING — no item half-moved, no
// dangling promotion tag — and the store must reopen fully readable with
// retrieval intact. SQLite's tx atomicity covers hard process death; this
// proves the promotion path actually stays inside ONE transaction and its
// error path rolls back rather than leaking a partial batch.
func TestLTMPromotionCrashSafeNoCorruption(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seedPromotable(t, s, 4)

	// Crash after the second per-item update, INSIDE the tx.
	calls := 0
	promoteFaultHook = func() error {
		calls++
		if calls == 2 {
			return errors.New("injected crash mid-promotion")
		}
		return nil
	}
	defer func() { promoteFaultHook = nil }()
	if _, _, perr := s.Promote(ctx); perr == nil {
		t.Fatal("the injected fault must surface as a promotion error")
	}
	promoteFaultHook = nil

	// All-or-nothing: NOTHING promoted, no promotion tag leaked.
	var ltm, tagged int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_items WHERE tier=?`, string(LTM)).Scan(&ltm); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_items WHERE promotion_id != ''`).Scan(&tagged); err != nil {
		t.Fatal(err)
	}
	if ltm != 0 || tagged != 0 {
		t.Fatalf("interrupted promotion must be all-or-nothing: %d LTM, %d tagged", ltm, tagged)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen: no corruption — every item readable, retrieval works, and a
	// clean re-promotion succeeds whole.
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("store must reopen after an interrupted promotion: %v", err)
	}
	defer func() { _ = s2.Close() }()
	items, err := s2.Retrieve(ctx, "promotable pattern", MTM)
	if err != nil || len(items) == 0 {
		t.Fatalf("retrieval must survive the interruption: %v (%d items)", err, len(items))
	}
	promoID, n, err := s2.Promote(ctx)
	if err != nil || n != 4 || promoID == "" {
		t.Fatalf("a clean re-promotion must promote the full batch: id=%q n=%d err=%v", promoID, n, err)
	}
	var ltm2 int
	if err := s2.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_items WHERE tier=? AND promotion_id=?`, string(LTM), promoID).Scan(&ltm2); err != nil {
		t.Fatal(err)
	}
	if ltm2 != 4 {
		t.Fatalf("the committed batch must be whole: %d/4 tagged LTM", ltm2)
	}
}
