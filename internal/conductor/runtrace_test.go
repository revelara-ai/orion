package conductor

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

func seedRun(t *testing.T, s *contextstore.Store, pid, runID string, events []contextstore.RunEvent) {
	t.Helper()
	ctx := context.Background()
	for _, e := range events {
		e.ProjectID, e.RunID = pid, runID
		if err := s.AppendRunEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
}

// TestSummarizeRunDerivesMetrics (or-kzf.3): the persisted trace yields the
// run's metrics — tasks, attempts, first-pass proof rate, completion.
func TestSummarizeRunDerivesMetrics(t *testing.T) {
	ctx := context.Background()
	s, pid := surfaceStore(t)
	seedRun(t, s, pid, "run-1", []contextstore.RunEvent{
		{Phase: "Run", Status: "running", Detail: "started"},
		// t1: first-pass Accept.
		{TaskID: "t1", Phase: "Generate", Status: "running"},
		{TaskID: "t1", Phase: "Prove", Status: "done", Detail: "Accept"},
		// t2: two attempts, accepted on the second.
		{TaskID: "t2", Phase: "Generate", Status: "running"},
		{TaskID: "t2", Phase: "Prove", Status: "warn", Detail: "Reject (attempt 1/3) — analyzing failure, refining"},
		{TaskID: "t2", Phase: "Generate", Status: "running", Detail: "attempt 2/3"},
		{TaskID: "t2", Phase: "Prove", Status: "done", Detail: "Accept (attempt 2)"},
		{Phase: "Run", Status: "done", Detail: "finished"},
	})

	sum, err := SummarizeRun(ctx, s, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Tasks != 2 || sum.Attempts != 3 {
		t.Fatalf("want 2 tasks / 3 attempts, got %+v", sum)
	}
	if sum.FirstPassAccepts != 1 {
		t.Fatalf("only t1 proved first-pass, got %d", sum.FirstPassAccepts)
	}
	if sum.ProvenTasks != 2 || sum.Failed {
		t.Fatalf("both tasks proved and the run closed: %+v", sum)
	}
	if sum.AttemptsPerTask() != 1.5 || sum.FirstPassRate() != 0.5 {
		t.Fatalf("derived rates wrong: %+v", sum)
	}

	// A run that never closed reads as failed.
	seedRun(t, s, pid, "run-open", []contextstore.RunEvent{{Phase: "Run", Status: "running"}})
	open, err := SummarizeRun(ctx, s, "run-open")
	if err != nil || !open.Failed {
		t.Fatalf("an unclosed run is a failed run: %+v (%v)", open, err)
	}
}

// TestDriftSignalNamesDegradation (or-kzf.3): rising attempts/task or a
// falling first-pass rate is named; improvement or stasis is silent.
func TestDriftSignalNamesDegradation(t *testing.T) {
	prev := RunTraceSummary{Tasks: 4, Attempts: 4, FirstPassAccepts: 4}
	worse := RunTraceSummary{Tasks: 4, Attempts: 9, FirstPassAccepts: 2}
	sig := DriftSignal(prev, worse)
	if !strings.Contains(sig, "refinement pressure rising") || !strings.Contains(sig, "first-pass proof rate falling") {
		t.Fatalf("both axes must be named: %q", sig)
	}
	if DriftSignal(prev, prev) != "" {
		t.Fatal("stasis is not drift")
	}
	better := RunTraceSummary{Tasks: 4, Attempts: 4, FirstPassAccepts: 4}
	if DriftSignal(worse, better) != "" {
		t.Fatal("improvement is not drift")
	}
	if DriftSignal(RunTraceSummary{}, worse) != "" {
		t.Fatal("no baseline → no signal")
	}
}

// TestListRunIDsNewestFirst: the drift comparison axis.
func TestListRunIDsNewestFirst(t *testing.T) {
	ctx := context.Background()
	s, pid := surfaceStore(t)
	seedRun(t, s, pid, "run-a", []contextstore.RunEvent{{Phase: "Run", Status: "running"}})
	seedRun(t, s, pid, "run-b", []contextstore.RunEvent{{Phase: "Run", Status: "running"}})
	ids, err := s.ListRunIDs(ctx, pid, 10)
	if err != nil || len(ids) != 2 || ids[0] != "run-b" || ids[1] != "run-a" {
		t.Fatalf("want [run-b run-a], got %v (%v)", ids, err)
	}
}
