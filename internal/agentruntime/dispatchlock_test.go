package agentruntime

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/a2a"
)

// countingAgent records how many times it actually runs.
type countingAgent struct{ runs *atomic.Int32 }

func (a countingAgent) Role() string { return "once" }
func (a countingAgent) Run(ctx context.Context, _ a2a.Request) (a2a.EvidenceClaim, error) {
	a.runs.Add(1)
	time.Sleep(60 * time.Millisecond) // hold the assignment so concurrent dispatch overlaps
	return a2a.EvidenceClaim{AssertionStatus: "ok"}, nil
}

// TestDispatchIsIdempotentNoDoubleAssign: many concurrent dispatches of the SAME
// task yield exactly one active assignment — the agent runs once and every caller
// shares the result. Run with -race.
func TestDispatchIsIdempotentNoDoubleAssign(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)

	var runs atomic.Int32
	reg := NewRegistry()
	reg.Register("once", func() Agent { return countingAgent{runs: &runs} })
	d := NewDispatcher(reg, s, 5*time.Second)

	tid := seedTask(t, s)
	req := a2a.Request{Role: "once", Obligation: a2a.ProofObligation{TaskID: tid}}

	const n = 12
	var wg sync.WaitGroup
	claims := make([]a2a.EvidenceClaim, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			claims[i], errs[i] = d.Dispatch(ctx, req, "attempt-1")
		}(i)
	}
	wg.Wait()

	if got := runs.Load(); got != 1 {
		t.Fatalf("agent ran %d times for one task — double-assigned (want exactly 1)", got)
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("dispatch %d errored: %v", i, errs[i])
		}
		if claims[i].AssertionStatus != "ok" {
			t.Fatalf("dispatch %d got an empty/mismatched claim: %+v", i, claims[i])
		}
	}
}

// TestSequentialDispatchStillRuns: the per-task lock only dedups concurrent
// dispatch — sequential dispatches of the same task each run (lock released).
func TestSequentialDispatchStillRuns(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	var runs atomic.Int32
	reg := NewRegistry()
	reg.Register("once", func() Agent { return countingAgent{runs: &runs} })
	d := NewDispatcher(reg, s, 5*time.Second)
	tid := seedTask(t, s)
	req := a2a.Request{Role: "once", Obligation: a2a.ProofObligation{TaskID: tid}}

	if _, err := d.Dispatch(ctx, req, "a1"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Dispatch(ctx, req, "a2"); err != nil {
		t.Fatal(err)
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("sequential dispatch ran %d times, want 2", got)
	}
}
