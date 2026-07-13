package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// seedProject writes one named project into the data dir's context store.
func seedProject(t *testing.T, dataDir, name string) {
	t.Helper()
	s, err := contextstore.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.WithTx(context.Background(), func(tx *contextstore.Tx) error {
		_, e := tx.Projects().Create(context.Background(), name, "intent "+name, "http-service")
		return e
	}); err != nil {
		t.Fatal(err)
	}
}

// projectNames lists the store's project names.
func projectNames(t *testing.T, dataDir string) []string {
	t.Helper()
	s, err := contextstore.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	var names []string
	if err := s.WithTx(context.Background(), func(tx *contextstore.Tx) error {
		projs, e := tx.Projects().List(context.Background())
		for _, p := range projs {
			names = append(names, p.Name)
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	return names
}

// TestBackupRestoreRoundTrip (or-hy4 DONE-WHEN): `orion backup` produces a
// restorable snapshot of both stores and `orion restore` round-trips — state
// written AFTER the backup is rolled back by the restore, and pre-backup
// state survives.
func TestBackupRestoreRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ORION_DATA_DIR", dataDir)
	seedProject(t, dataDir, "keep-me")

	dest := filepath.Join(t.TempDir(), "snap")
	if code := cmdBackup([]string{dest}); code != 0 {
		t.Fatalf("backup exited %d", code)
	}
	// BOTH stores must be in the snapshot — a context-store-only backup is not
	// the A17 deliverable.
	for _, f := range []string{"orion.db", "memory.db"} {
		if _, err := os.Stat(filepath.Join(dest, f)); err != nil {
			t.Fatalf("backup must snapshot %s: %v", f, err)
		}
	}

	// Post-backup mutation — the restore must roll this back.
	seedProject(t, dataDir, "post-backup-junk")

	if code := cmdRestore([]string{dest}); code != 0 {
		t.Fatalf("restore exited %d", code)
	}
	names := projectNames(t, dataDir)
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "keep-me") {
		t.Fatalf("pre-backup state must survive the round-trip, got %v", names)
	}
	if strings.Contains(joined, "post-backup-junk") {
		t.Fatalf("post-backup state must be rolled back by restore, got %v", names)
	}
}

// A restore from a dir that is not a backup refuses before touching anything.
func TestRestoreRefusesNonBackupDir(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ORION_DATA_DIR", dataDir)
	seedProject(t, dataDir, "untouched")

	if code := cmdRestore([]string{t.TempDir()}); code == 0 {
		t.Fatal("restoring from an empty dir must fail")
	}
	if names := projectNames(t, dataDir); len(names) != 1 || names[0] != "untouched" {
		t.Fatalf("a refused restore must not touch the live store, got %v", names)
	}
	// And a missing argument is usage, not a panic.
	if code := cmdRestore(nil); code == 0 {
		t.Fatal("restore without an argument must fail with usage")
	}
}

// TestLogsTailShowsTrailingEvents (or-hy4 DONE-WHEN): `orion logs` tails the
// persisted run_events stream — the LAST n events, newest included, with the
// follow cursor pointing at the newest id.
func TestLogsTailShowsTrailingEvents(t *testing.T) {
	dataDir := t.TempDir()
	s, err := contextstore.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		if err := s.AppendRunEvent(ctx, contextstore.RunEvent{
			ProjectID: "p1", RunID: "r1", Phase: "Generate",
			Status: "running", Detail: "event-" + strconv.Itoa(i),
			CreatedAt: "2026-07-13T00:00:00Z",
		}); err != nil {
			t.Fatal(err)
		}
	}
	out, lastID, err := logsTail(ctx, s, "r1", 5)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "event-29") {
		t.Fatalf("the tail must include the NEWEST event, got:\n%s", out)
	}
	// Negative: an old event outside the tail window must NOT render — the
	// tail is bounded, not a full dump (that is `orion trace`).
	if strings.Contains(out, "event-3\n") {
		t.Fatalf("event 3 is outside a 5-line tail, got:\n%s", out)
	}
	if lastID == 0 {
		t.Fatal("the follow cursor must point at the newest event id")
	}
	// Unknown run: an empty tail, not an error.
	if out, _, err := logsTail(ctx, s, "no-such-run", 5); err != nil || !strings.Contains(out, "no events") {
		t.Fatalf("unknown run should render 'no events': out=%q err=%v", out, err)
	}
}
