package conductor

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/proof"
)

// TestOverlappingLeasesSerializeClusters (or-v9f.10): two INDEPENDENT clusters
// whose declared file scopes overlap must never run concurrently — the lease is
// the scheduler's invariant, not a hope that the LLM scoped correctly.
func TestOverlappingLeasesSerializeClusters(t *testing.T) {
	tasks := []orchestrator.PlanTask{
		{ID: "a1", FileScope: "internal/server/"},
		{ID: "b1", FileScope: "internal/server/,internal/obs/"},
	}
	clusters := []decomposer.TaskCluster{
		{Key: "A", Members: []string{"a1"}},
		{Key: "B", Members: []string{"b1"}},
	}
	var cur, maxObs int32
	runTask := func(task orchestrator.PlanTask, _ map[string]proof.Report) (taskResult, error) {
		c := atomic.AddInt32(&cur, 1)
		for {
			m := atomic.LoadInt32(&maxObs)
			if c <= m || atomic.CompareAndSwapInt32(&maxObs, m, c) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		atomic.AddInt32(&cur, -1)
		return taskResult{TaskID: task.ID, Verdict: "Accept"}, nil
	}

	results, err := runClusterDAG(clusters, tasks, 2, runTask, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("both clusters must complete, got %d results", len(results))
	}
	for _, r := range results {
		if r.Verdict != "Accept" {
			t.Errorf("lease serialization must not fail tasks: %s got %s", r.TaskID, r.Verdict)
		}
	}
	if maxObs != 1 {
		t.Errorf("overlapping leases must serialize: high-water concurrency %d, want 1", maxObs)
	}
}

// TestUndeclaredScopeTakesExclusiveLease: a cluster that declares nothing could
// touch anything — it holds the whole tree, conservatively.
func TestUndeclaredScopeTakesExclusiveLease(t *testing.T) {
	tasks := []orchestrator.PlanTask{
		{ID: "a1", FileScope: ""}, // undeclared
		{ID: "b1", FileScope: "docs/"},
	}
	clusters := []decomposer.TaskCluster{
		{Key: "A", Members: []string{"a1"}},
		{Key: "B", Members: []string{"b1"}},
	}
	var cur, maxObs int32
	runTask := func(task orchestrator.PlanTask, _ map[string]proof.Report) (taskResult, error) {
		c := atomic.AddInt32(&cur, 1)
		for {
			m := atomic.LoadInt32(&maxObs)
			if c <= m || atomic.CompareAndSwapInt32(&maxObs, m, c) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		atomic.AddInt32(&cur, -1)
		return taskResult{TaskID: task.ID, Verdict: "Accept"}, nil
	}
	results, err := runClusterDAG(clusters, tasks, 2, runTask, nil)
	if err != nil || len(results) != 2 {
		t.Fatalf("both clusters must complete: %v, %d results", err, len(results))
	}
	if maxObs != 1 {
		t.Errorf("an undeclared scope must serialize against everything: high-water %d, want 1", maxObs)
	}
}

// TestScopesOverlapMatrix: the prefix-overlap decision itself.
func TestScopesOverlapMatrix(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"internal/server/", "internal/server/", true},
		{"internal/", "internal/server/", true}, // one is a prefix of the other
		{"internal/server/", "internal/obs/", false},
		{"go.mod,cmd/", "cmd/api/", true}, // comma lists expand
		{"docs/", "internal/", false},
		{"", "docs/", true},  // undeclared = whole tree
		{"docs/", "", true},  // symmetric
	}
	for _, tc := range cases {
		if got := scopesOverlap(leaseSet(tc.a), leaseSet(tc.b)); got != tc.want {
			t.Errorf("scopesOverlap(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
