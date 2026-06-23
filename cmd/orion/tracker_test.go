package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// or-y0z: `orion tracker project` is wired and projects the current project's
// epic + tasks to <data-dir>/tracker.jsonl.
func TestCmdTrackerProjectsToJSONL(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORION_DATA_DIR", dir)
	ctx := context.Background()

	// Seed what `plan` would produce: a project + epic + tasks.
	store, err := contextstore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
		pid, e := tx.Projects().Create(ctx, "demo", "build a time service", "http-service")
		if e != nil {
			return e
		}
		sid, e := tx.Specs().CreateDraft(ctx, pid)
		if e != nil {
			return e
		}
		eid, e := tx.Epics().Create(ctx, pid, sid, "epic")
		if e != nil {
			return e
		}
		if _, e := tx.Tasks().Create(ctx, eid, "task-1", "internal/"); e != nil {
			return e
		}
		_, e = tx.Tasks().Create(ctx, eid, "task-2", "cmd/")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	if code := cmdTracker([]string{"project"}); code != 0 {
		t.Fatalf("cmdTracker project returned %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "tracker.jsonl")); err != nil {
		t.Fatalf("tracker.jsonl should be written: %v", err)
	}

	// Usage error on a missing/unknown subcommand.
	if code := cmdTracker(nil); code != 2 {
		t.Errorf("cmdTracker with no args returned %d, want 2", code)
	}
}
