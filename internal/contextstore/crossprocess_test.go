package contextstore

import (
	"context"
	"sync"
	"testing"
)

// TestCrossProcessQueuePromotionSerializes (or-v9f.22): two independent Store
// handles on the same directory simulate two orion PROCESSES. Concurrent queue
// promotion must serialize through SQLite — exactly one handle promotes, and
// exactly one project ends active. Deferred transactions raced this
// read-modify-write to a snapshot-upgrade error; immediate transactions make
// the second writer wait its turn.
func TestCrossProcessQueuePromotionSerializes(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	ctx := context.Background()

	// One delivered project, two queued — the queue head is up for grabs.
	if err := s1.WithTx(ctx, func(tx *Tx) error {
		a, e := tx.Projects().Create(ctx, "a", "build A", "http-service")
		if e != nil {
			return e
		}
		if e := tx.Projects().SetStatus(ctx, a, "delivered"); e != nil {
			return e
		}
		for _, intent := range []string{"build B", "build C"} {
			id, e := tx.Projects().Create(ctx, intent, intent, "http-service")
			if e != nil {
				return e
			}
			if e := tx.Projects().SetStatus(ctx, id, "queued"); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	promoted := make([]bool, 2)
	errs := make([]error, 2)
	for i, s := range []*Store{s1, s2} {
		wg.Add(1)
		go func(i int, s *Store) {
			defer wg.Done()
			_, p, e := s.ActivateNextQueued(ctx)
			promoted[i], errs[i] = p, e
		}(i, s)
	}
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Fatalf("handle %d: cross-process promotion must serialize, not error: %v", i, e)
		}
	}
	if promoted[0] == promoted[1] {
		t.Fatalf("exactly ONE handle must promote, got %v", promoted)
	}
	active, err := s1.WithTxResult(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("the single-active invariant must hold across processes, got %d active", active)
	}
}

// WithTxResult counts active projects (test helper kept package-local).
func (s *Store) WithTxResult(ctx context.Context) (int, error) {
	n := 0
	err := s.view(ctx, func(tx *Tx) error {
		ps, e := tx.Projects().ListByStatus(ctx, "active")
		n = len(ps)
		return e
	})
	return n, err
}
