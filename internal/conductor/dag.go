package conductor

import (
	"fmt"
	"strings"
	"sync"

	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/proof"
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
	Blocked         bool // a dependency did not Accept, so this task was not run
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

// runClusterDAG executes the cluster DAG with BOUNDED PARALLELISM (or-tcs.1.4): clusters whose
// dependency clusters have all reached Accept run CONCURRENTLY (up to maxConc), each in its own
// worktree; a cluster's tasks run SEQUENTIALLY (coupled by file scope), each proven independently
// (the generation⊥proof wall holds per node). A cluster whose dependency cluster did NOT Accept is
// Blocked — its tasks are recorded Blocked, never run. maxConc<=1 degrades to sequential. A graph
// cycle (caught by topoSort) is a hard error. runTask must be safe to call concurrently (shared
// store/memory writes are serialized by the caller's stateMu; the proofCache is per-cluster).
//
// preDispatch (nil-safe) is the deterministic actuation gate (or-v9f.14): consulted before each
// cluster dispatch; a refusal (e.g. the red button engaged) records the cluster's tasks Blocked —
// in-flight clusters finish gracefully, no new work starts.
func runClusterDAG(clusters []decomposer.TaskCluster, tasks []orchestrator.PlanTask, maxConc int,
	runTask func(task orchestrator.PlanTask, cache map[string]proof.Report) (taskResult, error),
	preDispatch func(clusterKey string) error,
) ([]taskResult, error) {
	if maxConc < 1 {
		maxConc = 1
	}
	order, err := topoSort(tasks) // validates the graph (cycle/dangling) + intra-cluster ordering
	if err != nil {
		return nil, err
	}
	clusterOf := map[string]string{}
	for _, cl := range clusters {
		for _, m := range cl.Members {
			clusterOf[m] = cl.Key
		}
	}
	members := map[string][]orchestrator.PlanTask{} // clusterKey -> its tasks, in topo order
	for _, t := range order {
		members[clusterOf[t.ID]] = append(members[clusterOf[t.ID]], t)
	}
	deps := map[string]map[string]bool{} // clusterKey -> set of dependency clusterKeys
	for _, cl := range clusters {
		deps[cl.Key] = map[string]bool{}
	}
	for _, t := range tasks {
		ck := clusterOf[t.ID]
		for _, d := range t.DependsOn {
			if dk := clusterOf[d]; dk != "" && dk != ck {
				deps[ck][dk] = true
			}
		}
	}

	// Cluster leases (or-v9f.10): the union of each member task's declared file
	// scope. The dispatch loop never runs two clusters with overlapping leases
	// concurrently — path leasing enforced by the scheduler, not trusted to the
	// declarations being right.
	leases := map[string][]string{}
	for _, cl := range clusters {
		var set []string
		declared := true
		for _, t := range members[cl.Key] {
			ls := leaseSet(t.FileScope)
			if ls == nil {
				declared = false // one undeclared task -> the cluster leases the whole tree
				break
			}
			set = append(set, ls...)
		}
		if declared {
			leases[cl.Key] = set
		} // else leases[cl.Key] stays nil = exclusive
	}

	var mu sync.Mutex
	state := map[string]string{}  // clusterKey -> ""(pending)|running|Accept|Reject|Blocked
	accepted := map[string]bool{} // taskID -> reached Accept (global)
	var collected []taskResult
	completed, inflight := 0, 0

	type done struct {
		key, verdict string
		results      []taskResult
		err          error
	}
	doneCh := make(chan done, len(clusters)) // buffered → a goroutine never blocks sending (no leak on early return)

	// depState reports whether ck's dependency clusters are all Accept (ready) or any did not
	// Accept (blocked); neither when a dependency is still pending.
	depState := func(ck string) (ready, blocked bool) {
		for dk := range deps[ck] {
			switch state[dk] {
			case "Accept":
			case "Reject", "Blocked":
				return false, true
			default:
				return false, false
			}
		}
		return true, false
	}

	for completed < len(clusters) {
		mu.Lock()
		for _, cl := range clusters {
			if state[cl.Key] != "" {
				continue
			}
			ready, blocked := depState(cl.Key)
			if blocked {
				state[cl.Key] = "Blocked"
				for _, t := range members[cl.Key] {
					collected = append(collected, taskResult{TaskID: t.ID, Blocked: true, Verdict: "Blocked"})
				}
				completed++
				continue
			}
			if !ready || inflight >= maxConc {
				continue
			}
			if preDispatch != nil {
				if gerr := preDispatch(cl.Key); gerr != nil {
					state[cl.Key] = "Blocked"
					for _, t := range members[cl.Key] {
						collected = append(collected, taskResult{TaskID: t.ID, Blocked: true, Verdict: "Blocked", FailureAnalysis: gerr.Error()})
					}
					completed++
					continue
				}
			}
			// Lease check (or-v9f.10): a cluster whose lease overlaps an in-flight
			// cluster's stays PENDING (not blocked) and re-tries after the next
			// completion releases the lease. Deadlock-free: an empty in-flight set
			// holds no leases.
			leased := false
			for other, st := range state {
				if st == "running" && other != cl.Key && scopesOverlap(leases[cl.Key], leases[other]) {
					leased = true
					break
				}
			}
			if leased {
				continue
			}
			state[cl.Key] = "running"
			inflight++
			acc := make(map[string]bool, len(accepted)) // snapshot: dep-cluster tasks are settled
			for k, v := range accepted {
				acc[k] = v
			}
			key, clusterTasks := cl.Key, members[cl.Key]
			go func() {
				cache := map[string]proof.Report{} // per-cluster cache → no cross-cluster race
				verdict := "Accept"
				var rs []taskResult
				for _, t := range clusterTasks {
					unmet := false
					for _, d := range t.DependsOn {
						if !acc[d] {
							unmet = true
							break
						}
					}
					if unmet {
						rs = append(rs, taskResult{TaskID: t.ID, Blocked: true, Verdict: "Blocked"})
						verdict = "Reject"
						continue
					}
					tr, terr := runTask(t, cache)
					if terr != nil {
						doneCh <- done{key: key, err: terr}
						return
					}
					if tr.TaskID == "" {
						tr.TaskID = t.ID
					}
					rs = append(rs, tr)
					if tr.Verdict == "Accept" {
						acc[t.ID] = true
					} else {
						verdict = "Reject"
					}
				}
				doneCh <- done{key: key, verdict: verdict, results: rs}
			}()
		}
		mu.Unlock()

		if completed >= len(clusters) {
			break
		}
		if inflight == 0 {
			return collected, fmt.Errorf("cluster scheduler stalled: no ready cluster and none in flight")
		}
		d := <-doneCh
		mu.Lock()
		inflight--
		if d.err != nil {
			mu.Unlock()
			return collected, d.err
		}
		state[d.key] = d.verdict
		collected = append(collected, d.results...)
		for _, r := range d.results {
			if r.Verdict == "Accept" {
				accepted[r.TaskID] = true
			}
		}
		completed++
		mu.Unlock()
	}
	return collected, nil
}

// ── file-scope leases (or-v9f.10) ────────────────────────────────────────────

// leaseSet expands a task's declared FileScope (comma-separated path prefixes)
// into a lease set. An UNDECLARED scope returns nil, which scopesOverlap treats
// as the whole tree — a cluster that declares nothing could touch anything, so
// it runs exclusively (conservative, fail-safe).
func leaseSet(fileScope string) []string {
	var out []string
	for _, p := range strings.Split(fileScope, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// scopesOverlap reports whether two lease sets can collide: any prefix pair
// where one contains the other. nil (undeclared) collides with everything.
func scopesOverlap(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, x := range a {
		for _, y := range b {
			if strings.HasPrefix(x, y) || strings.HasPrefix(y, x) {
				return true
			}
		}
	}
	return false
}
