package conductor

import (
	"fmt"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// taskResult is the outcome of building + proving one DAG task.
type taskResult struct {
	TaskID          string
	Report          proof.Report
	Verdict         string
	Closed          bool
	BuildDir        string
	Attempts        int
	FailureAnalysis string
	Alignment       AlignmentRecord
	Blocked         bool                      // a dependency did not Accept, so this task was not run
	art             sandbox.GeneratedArtifact // the proven artifact (for GenSpec-identical reuse)
}

// runDAG executes each task through runTask in dependency (topological) order.
// A task is GATED — recorded Blocked and never run — unless every task it
// DependsOn reached verdict "Accept". This replaces the single-Tasks[0] build:
// each task is still proven INDEPENDENTLY by runTask (the generation⊥proof wall
// holds per node — no task inherits another's green check). A dependency cycle is
// a hard error, not an infinite loop. Sequential by design; bounded parallelism
// is a later slice.
func runDAG(tasks []orchestrator.PlanTask, runTask func(orchestrator.PlanTask) (taskResult, error)) ([]taskResult, error) {
	order, err := topoSort(tasks)
	if err != nil {
		return nil, err
	}
	results := make([]taskResult, 0, len(order))
	byID := make(map[string]taskResult, len(order))
	for _, task := range order {
		blocked := false
		for _, dep := range task.DependsOn {
			if r, ok := byID[dep]; !ok || r.Verdict != "Accept" {
				blocked = true
				break
			}
		}
		if blocked {
			tr := taskResult{TaskID: task.ID, Blocked: true, Verdict: "Blocked"}
			byID[task.ID] = tr
			results = append(results, tr)
			continue
		}
		tr, rerr := runTask(task)
		if rerr != nil {
			return results, rerr
		}
		if tr.TaskID == "" {
			tr.TaskID = task.ID
		}
		byID[task.ID] = tr
		results = append(results, tr)
	}
	return results, nil
}

// topoSort returns the tasks in dependency order (each task after every task it
// DependsOn), via Kahn's algorithm with stable input ordering for determinism.
// Returns an error on a cycle or a dangling dependency.
func topoSort(tasks []orchestrator.PlanTask) ([]orchestrator.PlanTask, error) {
	byID := make(map[string]orchestrator.PlanTask, len(tasks))
	indeg := make(map[string]int, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
		indeg[t.ID] = 0
	}
	adj := map[string][]string{} // dep -> dependents
	for _, t := range tasks {
		for _, dep := range t.DependsOn {
			if _, ok := byID[dep]; !ok {
				return nil, fmt.Errorf("task %s depends on unknown task %s", t.ID, dep)
			}
			adj[dep] = append(adj[dep], t.ID)
			indeg[t.ID]++
		}
	}
	var queue []string
	for _, t := range tasks { // input order → deterministic schedule
		if indeg[t.ID] == 0 {
			queue = append(queue, t.ID)
		}
	}
	out := make([]orchestrator.PlanTask, 0, len(tasks))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		out = append(out, byID[id])
		for _, nb := range adj[id] {
			indeg[nb]--
			if indeg[nb] == 0 {
				queue = append(queue, nb)
			}
		}
	}
	if len(out) != len(tasks) {
		return nil, fmt.Errorf("task graph has a cycle (sorted %d of %d tasks)", len(out), len(tasks))
	}
	return out, nil
}
