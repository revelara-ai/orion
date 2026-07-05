package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/actuation"
	"github.com/revelara-ai/orion/internal/budget"
	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/proof"
)

// TestPreDispatchGateBlocksClusters: the scheduler consults the gate before
// dispatching each cluster — with the gate refusing (red button engaged), no
// cluster runs and every task records Blocked. Mid-execution abort is a
// first-class case, not a bar-time footnote.
func TestPreDispatchGateBlocksClusters(t *testing.T) {
	clusters := []decomposer.TaskCluster{
		{Key: "c1", Members: []string{"a"}},
		{Key: "c2", Members: []string{"b"}},
	}
	tasks := []orchestrator.PlanTask{{ID: "a"}, {ID: "b"}}
	ran := 0
	runTask := func(task orchestrator.PlanTask, _ map[string]proof.Report) (taskResult, error) {
		ran++
		return taskResult{TaskID: task.ID, Verdict: "Accept"}, nil
	}
	gate := func(key string) error { return fmt.Errorf("actuation halted: red button engaged (%q blocked)", key) }

	results, err := runClusterDAG(clusters, tasks, 2, runTask, gate)
	if err != nil {
		t.Fatalf("an engaged button must not error the scheduler, it blocks dispatch: %v", err)
	}
	if ran != 0 {
		t.Fatalf("no task may run under an engaged red button, ran %d", ran)
	}
	if len(results) != 2 {
		t.Fatalf("every task must be accounted for, got %d results", len(results))
	}
	for _, r := range results {
		if !r.Blocked {
			t.Errorf("task %s must record Blocked under the red button, got %+v", r.TaskID, r)
		}
	}
}

// TestPreDispatchNilGateRunsNormally: a nil gate is the no-button case.
func TestPreDispatchNilGateRunsNormally(t *testing.T) {
	clusters := []decomposer.TaskCluster{{Key: "c1", Members: []string{"a"}}}
	tasks := []orchestrator.PlanTask{{ID: "a"}}
	runTask := func(task orchestrator.PlanTask, _ map[string]proof.Report) (taskResult, error) {
		return taskResult{TaskID: task.ID, Verdict: "Accept"}, nil
	}
	results, err := runClusterDAG(clusters, tasks, 1, runTask, nil)
	if err != nil || len(results) != 1 || results[0].Verdict != "Accept" {
		t.Fatalf("nil gate must run normally: %v %+v", err, results)
	}
}

// TestGitToolGuardedByRedButton: while engaged, mutating git ops are refused at
// the deterministic gate; reads still work (diagnosis stays possible mid-halt).
func TestGitToolGuardedByRedButton(t *testing.T) {
	oc := orchestrator.NewWithStore(openStore(t))
	repo := initDogfoodRepo(t)
	t.Chdir(repo)
	rb := actuation.RedButton{Path: filepath.Join(oc.Store().Dir(), "red_button")}
	if err := rb.Engage(); err != nil {
		t.Fatal(err)
	}
	tool, _ := specTools(oc, nil, &changeSession{}, nil).Get("git")

	if out, err := tool.Run(context.Background(), json.RawMessage(`{"args":["status"]}`)); err != nil {
		t.Fatalf("read-only git op must pass under the red button: %v (%s)", err, out)
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"args":["commit","-m","x"]}`)); err == nil || !strings.Contains(err.Error(), "red button") {
		t.Fatalf("mutating git op must be refused at the gate while engaged, got: %v", err)
	}

	if err := rb.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"args":["commit","--allow-empty","-m","x"]}`)); err != nil {
		t.Fatalf("release must restore actuation: %v", err)
	}
}

// TestBeadsToolGuardedByRedButton: same gate, same semantics for the issue DB.
func TestBeadsToolGuardedByRedButton(t *testing.T) {
	oc := orchestrator.NewWithStore(openStore(t))
	repo := initBeadsRepo(t)
	t.Chdir(repo)
	stubBd(t, `echo "ok"`)
	rb := actuation.RedButton{Path: filepath.Join(oc.Store().Dir(), "red_button")}
	if err := rb.Engage(); err != nil {
		t.Fatal(err)
	}
	tool, _ := specTools(oc, nil, &changeSession{}, nil).Get("bd")

	if out, err := tool.Run(context.Background(), json.RawMessage(`{"args":["ready"]}`)); err != nil {
		t.Fatalf("read-only bd op must pass under the red button: %v (%s)", err, out)
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"args":["close","or-1"]}`)); err == nil || !strings.Contains(err.Error(), "red button") {
		t.Fatalf("mutating bd op must be refused at the gate while engaged, got: %v", err)
	}
}

// TestBudgetGateRefusesWhenHalted (or-v9f.18): a run at its ceiling dispatches
// no new clusters; without a ceiling the gate is a no-op.
func TestBudgetGateRefusesWhenHalted(t *testing.T) {
	if err := budgetGate(nil); err != nil {
		t.Fatalf("nil accountant must pass: %v", err)
	}
	free := budget.New()
	free.Record(1_000_000, 99)
	if err := budgetGate(free); err != nil {
		t.Fatalf("no ceiling must never halt: %v", err)
	}
	capped := budget.NewWithCeiling(budget.Ceiling{MaxTokens: 100})
	capped.Record(100, 0)
	err := budgetGate(capped)
	if err == nil || !strings.Contains(err.Error(), "budget gate") {
		t.Fatalf("a halted budget must refuse dispatch with a named reason, got: %v", err)
	}
}
