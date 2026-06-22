package conductor

import (
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
)

// The scheduler runs every task (not just Tasks[0]) and a task never runs before
// the tasks it DependsOn — the core of multi-task DAG execution (or-tcs.1.1).
func TestDAGSchedulerRunsBothTasksInDepOrder(t *testing.T) {
	tasks := []orchestrator.PlanTask{
		{ID: "B", DependsOn: []string{"A"}},
		{ID: "A"},
		{ID: "C", DependsOn: []string{"A"}},
	}
	var order []string
	run := func(tk orchestrator.PlanTask) (taskResult, error) {
		order = append(order, tk.ID)
		return taskResult{TaskID: tk.ID, Verdict: "Accept", Closed: true}, nil
	}
	results, err := runDAG(tasks, run)
	if err != nil {
		t.Fatalf("runDAG: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 task results, got %d", len(results))
	}
	idx := map[string]int{}
	for i, id := range order {
		idx[id] = i
	}
	if len(order) != 3 {
		t.Fatalf("expected all 3 tasks to run, ran %v", order)
	}
	if idx["A"] > idx["B"] || idx["A"] > idx["C"] {
		t.Fatalf("dependency order violated: A must run before B and C; order=%v", order)
	}
}

// A downstream task is GATED (not run) when a dependency does not reach Accept —
// no task builds on an unproven upstream.
func TestDAGSchedulerGatesDownstreamOnNonAccept(t *testing.T) {
	tasks := []orchestrator.PlanTask{
		{ID: "A"},
		{ID: "B", DependsOn: []string{"A"}},
	}
	run := func(tk orchestrator.PlanTask) (taskResult, error) {
		if tk.ID == "B" {
			t.Fatalf("B must NOT run while its dependency A is unproven")
		}
		return taskResult{TaskID: "A", Verdict: "Reject"}, nil
	}
	results, err := runDAG(tasks, run)
	if err != nil {
		t.Fatalf("runDAG: %v", err)
	}
	var b *taskResult
	for i := range results {
		if results[i].TaskID == "B" {
			b = &results[i]
		}
	}
	if b == nil || !b.Blocked {
		t.Fatalf("B should be recorded as Blocked when A did not Accept; results=%+v", results)
	}
}

// A dependency cycle is a hard error, not an infinite loop.
func TestDAGSchedulerDetectsCycle(t *testing.T) {
	tasks := []orchestrator.PlanTask{
		{ID: "A", DependsOn: []string{"B"}},
		{ID: "B", DependsOn: []string{"A"}},
	}
	run := func(orchestrator.PlanTask) (taskResult, error) {
		return taskResult{Verdict: "Accept"}, nil
	}
	if _, err := runDAG(tasks, run); err == nil {
		t.Fatal("expected a cycle error, got nil")
	}
}
