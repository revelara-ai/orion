package contextstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestSnapshotToRoundTrip (or-hy4): SnapshotTo produces a CONSISTENT,
// restorable single-file copy of the store — data written before the snapshot
// is readable from the snapshot file opened as a store, and data written
// AFTER the snapshot is absent from it (point-in-time, not a live link).
func TestSnapshotToRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	var projID string
	if err := s.WithTx(ctx, func(tx *Tx) error {
		var e error
		projID, e = tx.Projects().Create(ctx, "backup-proj", "keep this project", "http-service")
		return e
	}); err != nil {
		t.Fatal(err)
	}

	snapDir := t.TempDir()
	snapPath := filepath.Join(snapDir, DBFile)
	if err := s.SnapshotTo(ctx, snapPath); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// Post-snapshot write must NOT appear in the snapshot (point-in-time).
	if err := s.WithTx(ctx, func(tx *Tx) error {
		_, e := tx.Projects().Create(ctx, "after-snap", "must not be in the snapshot", "http-service")
		return e
	}); err != nil {
		t.Fatal(err)
	}

	restored, err := Open(snapDir)
	if err != nil {
		t.Fatalf("open snapshot as a store: %v", err)
	}
	defer func() { _ = restored.Close() }()
	var got Project
	if err := restored.WithTx(ctx, func(tx *Tx) error {
		var e error
		got, e = tx.Projects().Get(ctx, projID)
		return e
	}); err != nil || got.Name != "backup-proj" {
		t.Fatalf("pre-snapshot project must round-trip: %+v err=%v", got, err)
	}
	var projs []Project
	if err := restored.WithTx(ctx, func(tx *Tx) error {
		var e error
		projs, e = tx.Projects().List(ctx)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	for _, p := range projs {
		if p.Name == "after-snap" {
			t.Fatal("a post-snapshot write leaked into the snapshot — not point-in-time")
		}
	}
}

// SnapshotTo must REFUSE an existing target — a restore path must never be
// silently clobbered by a later backup (SQLite VACUUM INTO semantics, pinned).
func TestSnapshotToRefusesExistingTarget(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	target := filepath.Join(t.TempDir(), "orion.db")
	if err := os.WriteFile(target, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.SnapshotTo(ctx, target); err == nil {
		t.Fatal("snapshot onto an existing file must error, never clobber")
	}
}
