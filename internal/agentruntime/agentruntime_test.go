package agentruntime

import (
	"context"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/a2a"
	"github.com/revelara-ai/orion/internal/contextstore"
)

// seedTask creates a project/spec/epic/task and returns the task id.
func seedTask(t *testing.T, s *contextstore.Store) string {
	t.Helper()
	ctx := context.Background()
	var taskID string
	err := s.WithTx(ctx, func(tx *contextstore.Tx) error {
		pid, _ := tx.Projects().Create(ctx, "demo", "build a time service", "http-service")
		sid, _ := tx.Specs().CreateDraft(ctx, pid)
		eid, _ := tx.Epics().Create(ctx, pid, sid, "epic")
		var e error
		taskID, e = tx.Tasks().Create(ctx, eid, "task", "cmd/x")
		return e
	})
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
	return taskID
}

func openStore(t *testing.T) *contextstore.Store {
	t.Helper()
	s, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestDispatchAndRecordAttempt: dispatch spawns a generator, runs it, and records
// a task_attempt carrying the untrusted EvidenceClaim — and crucially does NOT
// create a proof (the claim is never a verdict).
func TestDispatchAndRecordAttempt(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	taskID := seedTask(t, s)

	reg := DefaultRegistry()
	d := NewDispatcher(reg, s, 5*time.Second)

	req := a2a.Request{
		CorrelationID: "corr-1",
		Role:          RoleGoGenerator,
		Intent:        a2a.Intent{Summary: "build a time service"},
		Obligation:    a2a.ProofObligation{TaskID: taskID, Clauses: []string{"returns time"}},
	}
	claim, err := d.Dispatch(ctx, req, "attempt-1")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if claim.AssertionStatus == "" {
		t.Fatal("expected a non-empty assertion status on the claim")
	}
	if claim.Trusted() {
		t.Fatal("EvidenceClaim must be untrusted")
	}
	if claim.TaskID != taskID || claim.CorrelationID != "corr-1" {
		t.Fatalf("claim ids not stamped from request: %+v", claim)
	}

	attempts, err := s.AttemptCount(ctx, taskID)
	if err != nil {
		t.Fatalf("attempt count: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("task_attempts=%d, want 1", attempts)
	}

	// Trust wall: dispatch must NOT have produced a verdict/proof.
	proofs, err := s.ProofCount(ctx, taskID)
	if err != nil {
		t.Fatalf("proof count: %v", err)
	}
	if proofs != 0 {
		t.Fatalf("dispatch created %d proofs; the EvidenceClaim must never be a verdict", proofs)
	}

	// Fleet reflects the activity.
	fleet := reg.Fleet()
	if len(fleet) != 1 || fleet[0].TaskID != taskID || fleet[0].Status != StatusDone {
		t.Fatalf("fleet snapshot = %+v, want one done entry for the task", fleet)
	}
}

// slowAgent blocks until its context is cancelled, then returns the ctx error.
type slowAgent struct{}

func (slowAgent) Role() string { return "slow" }
func (slowAgent) Run(ctx context.Context, _ a2a.Request) (a2a.EvidenceClaim, error) {
	<-ctx.Done()
	return a2a.EvidenceClaim{}, ctx.Err()
}

// TestDispatchHonorsDeadline: a hung agent is timed out, dispatch errors, and no
// attempt is recorded.
func TestDispatchHonorsDeadline(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	taskID := seedTask(t, s)

	reg := NewRegistry()
	reg.Register("slow", func() Agent { return slowAgent{} })
	d := NewDispatcher(reg, s, 50*time.Millisecond)

	req := a2a.Request{Role: "slow", Obligation: a2a.ProofObligation{TaskID: taskID}}
	start := time.Now()
	if _, err := d.Dispatch(ctx, req, "attempt-1"); err == nil {
		t.Fatal("expected deadline error from a hung agent")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("dispatch took %s; deadline not enforced", elapsed)
	}
	if n, _ := s.AttemptCount(ctx, taskID); n != 0 {
		t.Fatalf("attempts=%d, want 0 (failed dispatch records nothing)", n)
	}
	if fleet := reg.Fleet(); len(fleet) != 1 || fleet[0].Status != StatusFailed {
		t.Fatalf("fleet = %+v, want one failed entry", fleet)
	}
}

// TestHandleCancel: cancelling a handle marks it cancelled.
func TestHandleCancel(t *testing.T) {
	reg := DefaultRegistry()
	h, err := reg.Spawn(RoleGoGenerator, "task-1")
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	h.Cancel()
	if h.Status() != StatusCancelled {
		t.Fatalf("status = %q, want cancelled", h.Status())
	}
}

// TestUnknownRoleErrors: dispatching an unregistered role errors.
func TestUnknownRoleErrors(t *testing.T) {
	s := openStore(t)
	d := NewDispatcher(NewRegistry(), s, time.Second)
	if _, err := d.Dispatch(context.Background(), a2a.Request{Role: "nope"}, "k"); err == nil {
		t.Fatal("expected error for unknown role")
	}
}
