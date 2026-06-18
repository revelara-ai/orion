package contextstore

import (
	"context"
	"errors"
	"testing"
)

// TestRecallRebuildsContextAfterAgentKill simulates an agent killed mid-WRITE and
// proves the harness-reliability contract: committed work survives, the
// uncommitted (killed) write rolls back leaving no corruption, and a restarted
// agent rebuilds context via Recall and resumes — adding exactly one attempt and
// re-asking zero decisions.
func TestRecallRebuildsContextAfterAgentKill(t *testing.T) {
	s := mustOpen(t)
	ctx := context.Background()

	// Seed an accepted spec with answered decisions, an epic, and a task.
	var specID, taskID string
	if err := s.WithTx(ctx, func(tx *Tx) error {
		pid, _ := tx.Projects().Create(ctx, "demo", "build a time service")
		var err error
		specID, err = tx.Specs().CreateDraft(ctx, pid)
		if err != nil {
			return err
		}
		for _, kv := range [][2]string{{"response_format", "json"}, {"timezone", "UTC"}, {"port", "8080"}, {"route", "/time"}} {
			if _, err := tx.Decisions().Create(ctx, pid, specID, kv[0], kv[1], "precise", false); err != nil {
				return err
			}
		}
		eid, err := tx.Epics().Create(ctx, pid, specID, "epic")
		if err != nil {
			return err
		}
		taskID, err = tx.Tasks().Create(ctx, eid, "implement service", "cmd/svc")
		return err
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Pre-crash: attempt-1 + an artifact commit atomically.
	if err := s.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.Attempts().Create(ctx, taskID, "attempt-1"); err != nil {
			return err
		}
		_, err := tx.Artifacts().Create(ctx, taskID, "code", "cmd/svc/main.go", "hash-1")
		return err
	}); err != nil {
		t.Fatalf("attempt-1: %v", err)
	}

	// The agent is KILLED mid-WRITE of attempt-2: the transaction never commits.
	killed := errors.New("agent killed mid-write")
	if err := s.WithTx(ctx, func(tx *Tx) error {
		if _, err := tx.Attempts().CreateWithClaim(ctx, taskID, "attempt-2", `{"assertion_status":"partial"}`); err != nil {
			return err
		}
		return killed // crash before commit → rollback
	}); !errors.Is(err, killed) {
		t.Fatalf("expected killed error, got %v", err)
	}

	// State must be intact: the rolled-back attempt-2 left nothing.
	if got := countRows(t, s, "task_attempts"); got != 1 {
		t.Fatalf("after kill task_attempts=%d, want 1 (attempt-2 rolled back)", got)
	}

	// Restart: a replacement agent rebuilds context via Recall.
	fb, err := s.Recall(ctx, taskID)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if fb.Spec.ID != specID || fb.Project.Intent != "build a time service" {
		t.Fatalf("recall did not rebuild spec/project: %+v", fb)
	}
	if len(fb.Decisions) != 4 {
		t.Fatalf("recall decisions = %d, want 4 (resume re-asks nothing)", len(fb.Decisions))
	}
	if len(fb.Attempts) != 1 || fb.Attempts[0].IdempotencyKey != "attempt-1" {
		t.Fatalf("recall attempts = %+v, want [attempt-1]", fb.Attempts)
	}
	if len(fb.Artifacts) != 1 || fb.Artifacts[0].ContentHash != "hash-1" {
		t.Fatalf("recall artifacts = %+v, want the committed pre-crash artifact", fb.Artifacts)
	}

	// Resume: idempotency check shows attempt-1 already applied; complete attempt-2.
	if err := s.WithTx(ctx, func(tx *Tx) error {
		done, err := tx.Attempts().HasAttempt(ctx, taskID, "attempt-1")
		if err != nil {
			return err
		}
		if !done {
			t.Fatal("attempt-1 should be detected as already applied")
		}
		_, err = tx.Attempts().CreateWithClaim(ctx, taskID, "attempt-2", `{"assertion_status":"implemented"}`)
		return err
	}); err != nil {
		t.Fatalf("resume: %v", err)
	}

	// Exactly +1 attempt, zero new decisions.
	if got := countRows(t, s, "task_attempts"); got != 2 {
		t.Fatalf("after resume task_attempts=%d, want 2", got)
	}
	if got := countRows(t, s, "decisions"); got != 4 {
		t.Fatalf("decisions=%d, want 4 (no new decisions on resume)", got)
	}
}
