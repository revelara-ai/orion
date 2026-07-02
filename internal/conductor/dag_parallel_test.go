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

// TestIndependentClustersRunInParallelBounded (or-tcs.1.4): independent clusters run CONCURRENTLY
// up to the bound; a dependent cluster waits until its dependency cluster Accepts; concurrency
// never exceeds N. Run under -race.
func TestIndependentClustersRunInParallelBounded(t *testing.T) {
	// Disjoint file scopes: undeclared scopes lease the whole tree and would
	// serialize (or-v9f.10) — this test's subject is the parallelism of
	// independent, non-overlapping clusters.
	tasks := []orchestrator.PlanTask{
		{ID: "a1", FileScope: "a/"}, {ID: "b1", FileScope: "b/"}, {ID: "c1", FileScope: "c/"},
		{ID: "d1", FileScope: "d/", DependsOn: []string{"a1"}}, // cluster D depends on cluster A
	}
	clusters := []decomposer.TaskCluster{
		{Key: "A", Members: []string{"a1"}},
		{Key: "B", Members: []string{"b1"}},
		{Key: "C", Members: []string{"c1"}},
		{Key: "D", Members: []string{"d1"}},
	}
	const N = 2
	var cur, maxObs int32
	var mu sync.Mutex
	var order []string

	runTask := func(task orchestrator.PlanTask, _ map[string]proof.Report) (taskResult, error) {
		c := atomic.AddInt32(&cur, 1)
		for { // record the high-water concurrency
			m := atomic.LoadInt32(&maxObs)
			if c <= m || atomic.CompareAndSwapInt32(&maxObs, m, c) {
				break
			}
		}
		mu.Lock()
		order = append(order, task.ID)
		mu.Unlock()
		time.Sleep(40 * time.Millisecond) // force overlap so concurrency is observable
		atomic.AddInt32(&cur, -1)
		return taskResult{TaskID: task.ID, Verdict: "Accept"}, nil
	}

	results, err := runClusterDAG(clusters, tasks, N, runTask, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 4 {
		t.Fatalf("want 4 task results, got %d", len(results))
	}
	if maxObs > N {
		t.Errorf("concurrency exceeded the bound: observed %d > N=%d", maxObs, N)
	}
	if maxObs < 2 {
		t.Errorf("independent clusters did not run concurrently (high-water = %d, want >=2)", maxObs)
	}
	ai, di := indexOf(order, "a1"), indexOf(order, "d1")
	if ai < 0 || di < 0 || di < ai {
		t.Errorf("d1 (depends on a1) must run AFTER a1: order=%v", order)
	}
	for _, r := range results {
		if r.Verdict != "Accept" {
			t.Errorf("task %s should Accept, got %s", r.TaskID, r.Verdict)
		}
	}
}

// TestClusterBlockedWhenDependencyRejects: a cluster whose dependency cluster did NOT Accept is
// Blocked — its tasks are recorded Blocked and never run.
func TestClusterBlockedWhenDependencyRejects(t *testing.T) {
	tasks := []orchestrator.PlanTask{{ID: "a1"}, {ID: "d1", DependsOn: []string{"a1"}}}
	clusters := []decomposer.TaskCluster{
		{Key: "A", Members: []string{"a1"}},
		{Key: "D", Members: []string{"d1"}},
	}
	var mu sync.Mutex
	ran := map[string]bool{}
	runTask := func(task orchestrator.PlanTask, _ map[string]proof.Report) (taskResult, error) {
		mu.Lock()
		ran[task.ID] = true
		mu.Unlock()
		v := "Accept"
		if task.ID == "a1" {
			v = "Reject" // cluster A fails
		}
		return taskResult{TaskID: task.ID, Verdict: v}, nil
	}
	results, err := runClusterDAG(clusters, tasks, 2, runTask, nil)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	d1ran := ran["d1"]
	mu.Unlock()
	if d1ran {
		t.Error("d1 must NOT run — its dependency cluster A rejected")
	}
	for _, r := range results {
		if r.TaskID == "d1" && !r.Blocked {
			t.Errorf("d1 must be Blocked, got %+v", r)
		}
	}
}

func indexOf(xs []string, want string) int {
	for i, x := range xs {
		if x == want {
			return i
		}
	}
	return -1
}
