package conductor

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/proof"
)

// or-tcs.2: the slot-fill order is critical-path priority, slice order on
// ties — a chain head (unblocks the most downstream work) outranks
// independent leaves regardless of slice position.
func TestDispatchOrderCriticalPathFirst(t *testing.T) {
	clusters := []decomposer.TaskCluster{
		{Key: "D"}, {Key: "A"}, {Key: "C"}, {Key: "B"},
	}
	// Chain C→B→A (C depends on B depends on A); D independent.
	deps := map[string]map[string]bool{
		"A": {}, "B": {"A": true}, "C": {"B": true}, "D": {},
	}
	pr := clusterPriorities(clusters, deps)
	if pr["A"] != 2 || pr["B"] != 1 || pr["C"] != 0 || pr["D"] != 0 {
		t.Fatalf("critical-path priorities wrong: %v", pr)
	}
	got := dispatchOrder(clusters, deps)
	want := []string{"A", "B", "D", "C"} // A(2), B(1), then slice order among the 0s: D before C
	for i, cl := range got {
		if cl.Key != want[i] {
			t.Fatalf("dispatch order: got %v want %v", keysOf(got), want)
		}
	}
}

func keysOf(cls []decomposer.TaskCluster) []string {
	out := make([]string, len(cls))
	for i, c := range cls {
		out[i] = c.Key
	}
	return out
}

// Acceptance: with 1 worker and 2 ready clusters, the slot goes to the
// chain head (priority), and dependencies stay respected end to end.
func TestSlotFillsByPriorityRespectingDeps(t *testing.T) {
	// Slice order deliberately puts the LOW-priority independent leaf first.
	clusters := []decomposer.TaskCluster{
		{Key: "leaf", Members: []string{"t-leaf"}},
		{Key: "head", Members: []string{"t-head"}},
		{Key: "tail", Members: []string{"t-tail"}},
	}
	tasks := []orchestrator.PlanTask{
		{ID: "t-leaf"},
		{ID: "t-head"},
		{ID: "t-tail", DependsOn: []string{"t-head"}}, // tail's cluster depends on head's
	}
	var mu sync.Mutex
	var started []string
	var running atomic.Int32
	res, err := runClusterDAG(clusters, tasks, 1, func(task orchestrator.PlanTask, _ map[string]proof.Report) (taskResult, error) {
		if running.Add(1) > 1 {
			t.Error("worker pool bound violated")
		}
		mu.Lock()
		started = append(started, task.ID)
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		running.Add(-1)
		return taskResult{TaskID: task.ID, Verdict: "Accept", Closed: true}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 {
		t.Fatalf("all 3 tasks must run: %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if started[0] != "t-head" {
		t.Fatalf("the single slot must go to the chain head (priority), got start order %v", started)
	}
	// Dependency respected: tail only ever after head.
	for i, id := range started {
		if id == "t-tail" {
			for j := i + 1; j < len(started); j++ {
				if started[j] == "t-head" {
					t.Fatalf("dependency violated: %v", started)
				}
			}
		}
	}
}
