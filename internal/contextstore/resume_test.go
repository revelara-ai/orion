package contextstore

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// resumeHelperMain runs in a forked copy of the test binary: it commits the
// generation attempt, signals readiness, then blocks until SIGKILLed — standing
// in for an in-flight agent that is killed mid-task.
func resumeHelperMain() {
	ctx := context.Background()
	dir := os.Getenv("ORION_RESUME_DIR")
	task := os.Getenv("ORION_RESUME_TASK")
	marker := os.Getenv("ORION_RESUME_MARKER")

	s, err := Open(dir)
	if err != nil {
		os.Exit(3)
	}
	if err := s.WithTx(ctx, func(tx *Tx) error {
		_, e := tx.Attempts().Create(ctx, task, "gen") // the committed side effect
		return e
	}); err != nil {
		os.Exit(4)
	}
	_ = os.WriteFile(marker, []byte("ready"), 0o644)
	time.Sleep(60 * time.Second) // wait to be killed
	os.Exit(0)
}

// TestResumeAfterSIGKILL: an in-flight agent is SIGKILLed after committing its
// generation attempt; a replacement reconstructs context via Recall and completes
// the task — exactly +1 attempt, +0 decisions, task reaches done.
func TestResumeAfterSIGKILL(t *testing.T) {
	if os.Getenv("ORION_RESUME_HELPER") == "1" {
		resumeHelperMain()
		return
	}

	ctx := context.Background()
	dir := t.TempDir()

	// Seed an accepted spec (decisions captured), epic, and task.
	var taskID string
	s := mustOpenAt(t, dir)
	if err := s.WithTx(ctx, func(tx *Tx) error {
		pid, _ := tx.Projects().Create(ctx, "demo", "build a time service", "http-service")
		sid, _ := tx.Specs().CreateDraft(ctx, pid)
		for _, kv := range [][2]string{{"response_format", "json"}, {"timezone", "UTC"}, {"port", "8080"}, {"route", "/time"}} {
			if _, e := tx.Decisions().Create(ctx, pid, sid, kv[0], kv[1], "precise", false); e != nil {
				return e
			}
		}
		eid, _ := tx.Epics().Create(ctx, pid, sid, "epic", "")
		var e error
		taskID, e = tx.Tasks().Create(ctx, eid, "implement", "cmd/")
		return e
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = s.Close() // let the subprocess own the DB while it runs

	// Fork an in-flight agent (this test binary, helper branch).
	marker := filepath.Join(dir, "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestResumeAfterSIGKILL$", "-test.timeout=120s")
	cmd.Env = append(os.Environ(),
		"ORION_RESUME_HELPER=1",
		"ORION_RESUME_DIR="+dir,
		"ORION_RESUME_TASK="+taskID,
		"ORION_RESUME_MARKER="+marker,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start agent: %v", err)
	}
	// Wait until the agent has committed its attempt.
	if !waitFile(marker, 30*time.Second) {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		t.Fatal("agent never committed its attempt")
	}
	// SIGKILL the in-flight agent (and its group).
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill: %v", err)
	}
	_, _ = cmd.Process.Wait()

	// Reopen: the committed generation attempt survived the kill.
	s2 := mustOpenAt(t, dir)
	if got := countRows(t, s2, "task_attempts"); got != 1 {
		t.Fatalf("pre-resume attempts = %d, want 1 (committed gen survived)", got)
	}
	if got := countRows(t, s2, "decisions"); got != 4 {
		t.Fatalf("decisions = %d, want 4", got)
	}
	if task, _ := s2.Task(ctx, taskID); task.Status == "done" {
		t.Fatal("task should not be done before resume")
	}

	// Resume: Recall rebuilds context; idempotency skips the committed gen; the
	// replacement records exactly one more attempt and closes the task — re-asking
	// zero decisions.
	fb, err := s2.Recall(ctx, taskID)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(fb.Decisions) != 4 || len(fb.Attempts) != 1 {
		t.Fatalf("recall bundle = %d decisions / %d attempts, want 4 / 1", len(fb.Decisions), len(fb.Attempts))
	}
	if err := s2.WithTx(ctx, func(tx *Tx) error {
		done, err := tx.Attempts().HasAttempt(ctx, taskID, "gen")
		if err != nil {
			return err
		}
		if !done {
			t.Fatal("gen attempt should be detected as already applied")
		}
		if _, e := tx.Attempts().Create(ctx, taskID, "complete"); e != nil { // +1
			return e
		}
		pid, e := tx.Proofs().Create(ctx, taskID, Proof{Mode: "converged", Verdict: "Accept", RunCount: 1})
		if e != nil {
			return e
		}
		return tx.Tasks().SetProofAndStatus(ctx, taskID, pid, "done")
	}); err != nil {
		t.Fatalf("resume: %v", err)
	}

	if got := countRows(t, s2, "task_attempts"); got != 2 {
		t.Fatalf("post-resume attempts = %d, want 2 (+1)", got)
	}
	if got := countRows(t, s2, "decisions"); got != 4 {
		t.Fatalf("decisions = %d, want 4 (+0 — no re-ask)", got)
	}
	if task, _ := s2.Task(ctx, taskID); task.Status != "done" {
		t.Fatalf("task status = %q, want done", task.Status)
	}
}

func mustOpenAt(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func waitFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
