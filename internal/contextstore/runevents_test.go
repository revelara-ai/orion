package contextstore

import (
	"context"
	"testing"
)

// TestRunEventsRoundTrip (or-v9f.16): append → incremental tail → latest-run
// resolution. The attach reader is built entirely on these primitives.
func TestRunEventsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	for _, e := range []RunEvent{
		{ProjectID: "p1", RunID: "run-a", Phase: "Run", Status: "running", Detail: "started"},
		{ProjectID: "p1", RunID: "run-a", TaskID: "T1", Phase: "Generate", Status: "done"},
		{ProjectID: "p1", RunID: "run-a", Phase: "Run", Status: "done", Detail: "finished"},
	} {
		if err := s.AppendRunEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	events, err := s.ListRunEventsAfter(ctx, "run-a", 0)
	if err != nil || len(events) != 3 {
		t.Fatalf("want 3 events, got %d (%v)", len(events), err)
	}
	if events[1].TaskID != "T1" || events[1].Phase != "Generate" {
		t.Fatalf("per-cluster attribution must persist: %+v", events[1])
	}
	// Incremental tail: only what's newer than the cursor.
	tail, err := s.ListRunEventsAfter(ctx, "run-a", events[1].ID)
	if err != nil || len(tail) != 1 || tail[0].Phase != "Run" || tail[0].Status != "done" {
		t.Fatalf("incremental tail broken: %+v (%v)", tail, err)
	}

	// Latest run resolves the newest run for the project.
	if err := s.AppendRunEvent(ctx, RunEvent{ProjectID: "p1", RunID: "run-b", Phase: "Run", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	id, ok, err := s.LatestRunID(ctx, "p1")
	if err != nil || !ok || id != "run-b" {
		t.Fatalf("latest run must be run-b, got %q ok=%v (%v)", id, ok, err)
	}
	if _, ok, _ := s.LatestRunID(ctx, "nope"); ok {
		t.Fatal("a project with no runs has no latest run")
	}
}
