package contextstore

import (
	"context"
	"errors"
	"testing"
)

func mustOpen(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func countRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestOpenUsesWAL: the store must run in WAL mode (crash-safe write path).
func TestOpenUsesWAL(t *testing.T) {
	s := mustOpen(t)
	var jm string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&jm); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if jm != "wal" {
		t.Fatalf("journal_mode=%q, want wal", jm)
	}
}

// TestTransactionalWrite: the atomic sequence (task_attempt + artifact + proof
// link) commits as one unit; a failure mid-sequence persists nothing.
func TestTransactionalWrite(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	var taskID string
	// Happy path: a full sequence commits atomically.
	err := s.WithTx(ctx, func(tx *Tx) error {
		pid, err := tx.Projects().Create(ctx, "demo", "build a time service", "http-service")
		if err != nil {
			return err
		}
		sid, err := tx.Specs().CreateDraft(ctx, pid)
		if err != nil {
			return err
		}
		eid, err := tx.Epics().Create(ctx, pid, sid, "epic-1")
		if err != nil {
			return err
		}
		taskID, err = tx.Tasks().Create(ctx, eid, "task-1", "cmd/x")
		if err != nil {
			return err
		}
		if _, err := tx.Attempts().Create(ctx, taskID, "idem-1"); err != nil {
			return err
		}
		if _, err := tx.Artifacts().Create(ctx, taskID, "code", "/p/main.go", "hash1"); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("happy WithTx: %v", err)
	}
	if got := countRows(t, s, "task_attempts"); got != 1 {
		t.Fatalf("task_attempts=%d, want 1", got)
	}
	if got := countRows(t, s, "artifacts"); got != 1 {
		t.Fatalf("artifacts=%d, want 1", got)
	}

	// Rollback path: a failing fn after writes persists nothing.
	wantErr := errors.New("boom")
	err = s.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.Attempts().Create(ctx, taskID, "idem-2"); err != nil {
			return err
		}
		if _, err := tx.Artifacts().Create(ctx, taskID, "code", "/p/other.go", "hash2"); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("WithTx error = %v, want %v", err, wantErr)
	}
	if got := countRows(t, s, "task_attempts"); got != 1 {
		t.Fatalf("after rollback task_attempts=%d, want 1 (rolled back)", got)
	}
	if got := countRows(t, s, "artifacts"); got != 1 {
		t.Fatalf("after rollback artifacts=%d, want 1 (rolled back)", got)
	}
}

// TestSurvivesRestart: data committed before Close is present after reopening the
// same directory — the resumability substrate (Story 31).
func TestSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	const intent = "Build an HTTP service that returns the current time."
	var pid string
	if err := s1.WithTx(ctx, func(tx *Tx) error {
		var e error
		pid, e = tx.Projects().Create(ctx, "demo", intent, "http-service")
		if e != nil {
			return e
		}
		_, e = tx.Specs().CreateDraft(ctx, pid)
		return e
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close 1: %v", err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("open 2: %v", err)
	}
	defer s2.Close()
	got, err := s2.Project(ctx, pid)
	if err != nil {
		t.Fatalf("reload project: %v", err)
	}
	if got.Intent != intent {
		t.Fatalf("intent after restart = %q, want %q", got.Intent, intent)
	}
	specs, err := s2.SpecsForProject(ctx, pid)
	if err != nil || len(specs) != 1 || specs[0].Status != "drafting" {
		t.Fatalf("spec after restart = %+v err=%v, want one drafting spec", specs, err)
	}
}

// TestDoneGateRejectsWithoutAcceptedProof: the proven/done transition is a DB
// constraint requiring a non-null proof_id whose verdict is Accept.
func TestDoneGateRejectsWithoutAcceptedProof(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	var taskID, rejectProofID, acceptProofID string
	if err := s.WithTx(ctx, func(tx *Tx) error {
		pid, _ := tx.Projects().Create(ctx, "demo", "x", "http-service")
		sid, _ := tx.Specs().CreateDraft(ctx, pid)
		eid, _ := tx.Epics().Create(ctx, pid, sid, "e")
		var err error
		taskID, err = tx.Tasks().Create(ctx, eid, "t", "scope")
		if err != nil {
			return err
		}
		rejectProofID, err = tx.Proofs().Create(ctx, taskID, Proof{Mode: "behavioral", Verdict: "Reject", RunCount: 1})
		if err != nil {
			return err
		}
		acceptProofID, err = tx.Proofs().Create(ctx, taskID, Proof{Mode: "behavioral", Verdict: "Accept", MutationScore: 0.9, RunCount: 3})
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// 1) done without any proof_id → rejected
	if err := s.WithTx(ctx, func(tx *Tx) error {
		return tx.Tasks().SetStatus(ctx, taskID, "done")
	}); err == nil {
		t.Fatal("expected done-gate to reject status=done with nil proof_id")
	}

	// 2) done with a Reject proof → rejected
	if err := s.WithTx(ctx, func(tx *Tx) error {
		return tx.Tasks().SetProofAndStatus(ctx, taskID, rejectProofID, "done")
	}); err == nil {
		t.Fatal("expected done-gate to reject status=done with a Reject proof")
	}

	// 3) proven with an Accept proof → allowed and persisted
	if err := s.WithTx(ctx, func(tx *Tx) error {
		return tx.Tasks().SetProofAndStatus(ctx, taskID, acceptProofID, "proven")
	}); err != nil {
		t.Fatalf("expected Accept proof to permit proven; got %v", err)
	}
	task, err := s.Task(ctx, taskID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status != "proven" || task.ProofID != acceptProofID {
		t.Fatalf("task = %+v, want status=proven proof_id=%s", task, acceptProofID)
	}
}

// TestFailureModeCanonicalKeyDeterministic: canonical_key is a deterministic
// hash over {category, component_type, symptom_class} (Story 30).
func TestFailureModeCanonicalKeyDeterministic(t *testing.T) {
	k1 := CanonicalKey("timeout", "http-handler", "unbounded-wait")
	k2 := CanonicalKey("timeout", "http-handler", "unbounded-wait")
	k3 := CanonicalKey("timeout", "http-handler", "other")
	if k1 != k2 {
		t.Fatalf("canonical key not deterministic: %q vs %q", k1, k2)
	}
	if k1 == k3 {
		t.Fatal("canonical key collided on differing inputs")
	}

	s := mustOpen(t)
	ctx := context.Background()
	// Same dimensions dedupe via UNIQUE(canonical_key).
	if err := s.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.FailureModes().Record(ctx, "", "timeout", "http-handler", "unbounded-wait"); err != nil {
			return err
		}
		_, err := tx.FailureModes().Record(ctx, "", "timeout", "http-handler", "unbounded-wait")
		return err
	}); err != nil {
		t.Fatalf("record failure modes: %v", err)
	}
	if got := countRows(t, s, "failure_modes"); got != 1 {
		t.Fatalf("failure_modes=%d, want 1 (deduped by canonical_key)", got)
	}
}
