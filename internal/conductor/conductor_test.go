package conductor

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

func seedTask(t *testing.T, s *contextstore.Store) string {
	t.Helper()
	ctx := context.Background()
	var taskID string
	if err := s.WithTx(ctx, func(tx *contextstore.Tx) error {
		pid, _ := tx.Projects().Create(ctx, "demo", "build a time service", "http-service")
		sid, _ := tx.Specs().CreateDraft(ctx, pid)
		eid, _ := tx.Epics().Create(ctx, pid, sid, "epic", "")
		var e error
		taskID, e = tx.Tasks().Create(ctx, eid, "implement", "cmd/")
		return e
	}); err != nil {
		t.Fatalf("seed: %v", err)
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

func taskStatus(t *testing.T, s *contextstore.Store, id string) string {
	t.Helper()
	task, err := s.Task(context.Background(), id)
	if err != nil {
		t.Fatalf("load task: %v", err)
	}
	return task.Status
}

// TestStateMachineRequiresProofToClose: a task cannot reach done without a proof
// whose verdict is Accept — not with no proof, not with a Reject proof.
func TestStateMachineRequiresProofToClose(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	sm := New(s)
	taskID := seedTask(t, s)

	if err := sm.Begin(ctx, taskID); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := sm.Validate(ctx, taskID); err != nil {
		t.Fatalf("validate: %v", err)
	}

	// No proof → cannot close.
	if err := sm.Close(ctx, taskID, ""); err == nil {
		t.Fatal("close with no proof should be refused")
	}

	// Reject proof → cannot close.
	rejectID, err := sm.RecordVerdict(ctx, taskID, truthalign.Converge(truthalign.ModeResult{Mode: "behavioral", Pass: false}))
	if err != nil {
		t.Fatalf("record reject: %v", err)
	}
	if err := sm.Close(ctx, taskID, rejectID); err == nil {
		t.Fatal("close with a Reject proof should be refused by the done-gate")
	}
	if taskStatus(t, s, taskID) == "done" {
		t.Fatal("task reached done without an Accept proof")
	}

	// Accept proof → closes.
	acceptID, err := sm.RecordVerdict(ctx, taskID, truthalign.Converge(truthalign.ModeResult{Mode: "behavioral", Pass: true}))
	if err != nil {
		t.Fatalf("record accept: %v", err)
	}
	if err := sm.Close(ctx, taskID, acceptID); err != nil {
		t.Fatalf("close with Accept proof failed: %v", err)
	}
	if got := taskStatus(t, s, taskID); got != "done" {
		t.Fatalf("status = %q, want done", got)
	}
}

// TestStateMachineRequiresAllThreeModes: the state machine closes a task only on
// a full behavioral+empirical+hazard Accept; a 2-of-3 report (missing hazard)
// converges Inconclusive and does not close.
func TestStateMachineRequiresAllThreeModes(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	sm := New(s)

	b := truthalign.ModeResult{Mode: "behavioral", Pass: true, Metrics: map[string]float64{"run_count": 1}}
	e := truthalign.ModeResult{Mode: "empirical", Pass: true, Metrics: map[string]float64{"run_count": 1}}
	h := truthalign.ModeResult{Mode: "hazard", Pass: true, Metrics: map[string]float64{"run_count": 1}}

	// Missing hazard → Inconclusive → not closed.
	twoMode := seedTask(t, s)
	report2 := proof.Report{Outcome: truthalign.ConvergeFull(b, e), Modes: []proof.ModeReport{{Result: b}, {Result: e}}}
	if closed, _ := sm.ProveAndCloseReport(ctx, twoMode, report2); closed {
		t.Fatal("2-of-3 modes must not close the task")
	}
	if taskStatus(t, s, twoMode) == "done" {
		t.Fatal("task done with only 2 modes")
	}

	// All three → Accept → closed.
	threeMode := seedTask(t, s)
	report3 := proof.Report{Outcome: truthalign.ConvergeFull(b, e, h), Modes: []proof.ModeReport{{Result: b}, {Result: e}, {Result: h}}}
	if closed, err := sm.ProveAndCloseReport(ctx, threeMode, report3); err != nil || !closed {
		t.Fatalf("all three modes should close: closed=%v err=%v", closed, err)
	}
	if taskStatus(t, s, threeMode) != "done" {
		t.Fatal("task not done after full 3-mode Accept")
	}
}

// TestProveAndCloseReportRejectsFailingProbe: an artifact that passes behavioral
// but fails the empirical probe converges Reject and is NOT closed (the 2-mode
// gate — passing tests are not enough).
func TestProveAndCloseReportRejectsFailingProbe(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	sm := New(s)
	taskID := seedTask(t, s)

	report := proof.Report{
		Outcome: truthalign.Converge(
			truthalign.ModeResult{Mode: "behavioral", Pass: true, Metrics: map[string]float64{"run_count": 1}},
			truthalign.ModeResult{Mode: "empirical", Pass: false, Metrics: map[string]float64{"empirical_pass_rate": 0, "run_count": 1}},
		),
		Modes: []proof.ModeReport{
			{Result: truthalign.ModeResult{Mode: "behavioral", Pass: true, Metrics: map[string]float64{"run_count": 1}}},
			{Result: truthalign.ModeResult{Mode: "empirical", Pass: false, Metrics: map[string]float64{"empirical_pass_rate": 0, "run_count": 1}},
				Detail: map[string]any{"port_open": false, "response_contract_satisfied": false}},
		},
	}
	closed, err := sm.ProveAndCloseReport(ctx, taskID, report)
	if err != nil {
		t.Fatalf("prove+close report: %v", err)
	}
	if closed {
		t.Fatal("a failing empirical probe must not close the task")
	}
	if taskStatus(t, s, taskID) == "done" {
		t.Fatal("task is done despite a failing empirical probe")
	}
	// The empirical proof row is persisted with its detail (for `orion proof show`).
	ep, err := s.ProofByTaskMode(ctx, taskID, "empirical")
	if err != nil {
		t.Fatalf("empirical proof not recorded: %v", err)
	}
	if ep.Verdict != "Reject" {
		t.Fatalf("empirical verdict = %s, want Reject", ep.Verdict)
	}
}

// TestProveAndCloseOnlyClosesOnAccept.
func TestProveAndCloseOnlyClosesOnAccept(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	sm := New(s)

	rejectTask := seedTask(t, s)
	closed, err := sm.ProveAndClose(ctx, rejectTask, truthalign.Converge(truthalign.ModeResult{Mode: "behavioral", Pass: false}))
	if err != nil {
		t.Fatalf("prove+close reject: %v", err)
	}
	if closed {
		t.Fatal("Reject verdict must not close the task")
	}
	if taskStatus(t, s, rejectTask) == "done" {
		t.Fatal("Reject task is done")
	}

	acceptTask := seedTask(t, s)
	closed, err = sm.ProveAndClose(ctx, acceptTask, truthalign.Converge(truthalign.ModeResult{Mode: "behavioral", Pass: true}))
	if err != nil || !closed {
		t.Fatalf("Accept verdict should close: closed=%v err=%v", closed, err)
	}
	if got := taskStatus(t, s, acceptTask); got != "done" {
		t.Fatalf("status = %q, want done", got)
	}
}
