package contextstore

import (
	"context"
	"errors"
	"testing"
)

// TestReservedProjectIsIdempotentAndQueueInvisible (or-v9f.15): the reserved
// brownfield holder is created once and never competes for the single active
// slot — the queue (Active/OldestQueued/QueuedProjects) never sees it.
func TestReservedProjectIsIdempotentAndQueueInvisible(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	var id1, id2 string
	mk := func() (string, error) {
		var id string
		err := store.WithTx(ctx, func(tx *Tx) error {
			var e error
			id, e = tx.Projects().GetOrCreateReserved(ctx, BrownfieldProjectName, "brownfield")
			return e
		})
		return id, err
	}
	if id1, err = mk(); err != nil {
		t.Fatal(err)
	}
	if id2, err = mk(); err != nil {
		t.Fatal(err)
	}
	if id1 == "" || id1 != id2 {
		t.Fatalf("reserved holder must be get-or-create idempotent: %q vs %q", id1, id2)
	}

	// With ONLY the reserved holder present, there is no active and no queued work.
	if err := store.WithTx(ctx, func(tx *Tx) error {
		if _, e := tx.Projects().Active(ctx); !errors.Is(e, ErrNotFound) {
			t.Errorf("reserved holder must not be active, got %v", e)
		}
		q, e := tx.Projects().ListByStatus(ctx, "queued")
		if e != nil {
			return e
		}
		if len(q) != 0 {
			t.Errorf("reserved holder must not be queued, got %v", q)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// A real active project created afterward is the in-flight work item; the
	// holder never shadowed it.
	if err := store.WithTx(ctx, func(tx *Tx) error {
		real, e := tx.Projects().Create(ctx, "real", "build a real service", "http-service")
		if e != nil {
			return e
		}
		active, e := tx.Projects().Active(ctx)
		if e != nil {
			return e
		}
		if active.ID != real {
			t.Errorf("the real project must be active, got holder=%v", active.ID == id1)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
