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

// TestSpendLedgerSurvivesReopen (or-v9f.28 DONE-WHEN c): cumulative spend
// persists across a store reopen — a restart never resets the ceiling basis.
func TestSpendLedgerSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendSpend(ctx, "p1", "run-1", "generator", "claude-opus-9", 5000, 1.25); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendSpend(ctx, "p1", "run-1", "conductor", "claude-haiku-4", 2000, 0.05); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	tok, dol, err := s2.SumSpend(ctx, "p1")
	if err != nil || tok != 7000 || dol != 1.30 {
		t.Fatalf("cumulative spend must survive reopen: %d tok $%.2f (%v)", tok, dol, err)
	}
	rows, err := s2.SpendByRole(ctx, "p1")
	if err != nil || len(rows) != 2 || rows[0].Role != "generator" || rows[0].Dollars != 1.25 {
		t.Fatalf("role/model breakdown must persist, biggest first: %+v (%v)", rows, err)
	}
	if tok, dol, _ := s2.SumSpend(ctx, "other"); tok != 0 || dol != 0 {
		t.Fatal("spend is per project")
	}
}
