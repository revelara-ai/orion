package contextstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestProjectStatusBackfillOnLegacyDB: opening a pre-queue DB (no projects.status
// column) must add the column once and backfill: the latest project was the
// implicit in-flight work item so it becomes active; every earlier, already-
// orphaned project is closed out as abandoned.
func TestProjectStatusBackfillOnLegacyDB(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, DBFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE projects (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, intent TEXT NOT NULL,
		project_type TEXT NOT NULL DEFAULT 'http-service',
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
		INSERT INTO projects VALUES
		('p-old','old','build OLD','http-service','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z'),
		('p-new','new','build NEW','http-service','2026-06-01T00:00:00Z','2026-06-01T00:00:00Z');`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	p, _, err := store.CurrentProjectSpec(ctx)
	if err == nil && p.Intent != "build NEW" {
		t.Errorf("latest legacy project must become active, got %q", p.Intent)
	}
	// CurrentProjectSpec may fail on the missing spec row; assert status directly.
	err = store.WithTx(ctx, func(tx *Tx) error {
		newer, e := tx.Projects().Get(ctx, "p-new")
		if e != nil {
			return e
		}
		older, e := tx.Projects().Get(ctx, "p-old")
		if e != nil {
			return e
		}
		if newer.Status != "active" {
			t.Errorf("latest legacy project: want active, got %q", newer.Status)
		}
		if older.Status != "abandoned" {
			t.Errorf("older legacy project: want abandoned, got %q", older.Status)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Re-opening must NOT re-run the backfill (the invariant enforcer is the
	// queue, not the migration): flip states and confirm they survive a reopen.
	if err := store.WithTx(ctx, func(tx *Tx) error {
		if e := tx.Projects().SetStatus(ctx, "p-new", "delivered"); e != nil {
			return e
		}
		return tx.Projects().SetStatus(ctx, "p-old", "active")
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()
	if err := store2.WithTx(ctx, func(tx *Tx) error {
		older, e := tx.Projects().Get(ctx, "p-old")
		if e != nil {
			return e
		}
		if older.Status != "active" {
			t.Errorf("backfill must not re-run on reopen: p-old want active, got %q", older.Status)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestActivateNextQueuedFIFO: promotion follows submit order, one at a time, and
// respects the single-active invariant.
func TestActivateNextQueuedFIFO(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	var ids [3]string
	if err := store.WithTx(ctx, func(tx *Tx) error {
		for i, intent := range []string{"build A", "build B", "build C"} {
			id, e := tx.Projects().Create(ctx, intent, intent, "http-service")
			if e != nil {
				return e
			}
			ids[i] = id
			if i > 0 { // A stays active; B and C queue behind it
				if e := tx.Projects().SetStatus(ctx, id, "queued"); e != nil {
					return e
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// An active project exists → no promotion, current returned.
	p, promoted, err := store.ActivateNextQueued(ctx)
	if err != nil || promoted || p.ID != ids[0] {
		t.Fatalf("with A active: want (A,false), got (%s,%v,%v)", p.Intent, promoted, err)
	}

	// Deliver A → B (not C) promotes.
	if err := store.WithTx(ctx, func(tx *Tx) error {
		return tx.Projects().SetStatus(ctx, ids[0], "delivered")
	}); err != nil {
		t.Fatal(err)
	}
	p, promoted, err = store.ActivateNextQueued(ctx)
	if err != nil || !promoted || p.ID != ids[1] {
		t.Fatalf("after A delivered: want (B,true), got (%s,%v,%v)", p.Intent, promoted, err)
	}
	if q, _ := store.QueuedProjects(ctx); len(q) != 1 || q[0].ID != ids[2] {
		t.Fatalf("queue after promoting B: want [C], got %v", q)
	}
}
