package agentruntime

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/a2a"
)

// trackingAgent records the peak number of concurrently-running instances.
type trackingAgent struct {
	cur  *atomic.Int32
	peak *atomic.Int32
}

func (a trackingAgent) Role() string { return "tracker" }
func (a trackingAgent) Run(ctx context.Context, _ a2a.Request) (a2a.EvidenceClaim, error) {
	n := a.cur.Add(1)
	for {
		p := a.peak.Load()
		if n <= p || a.peak.CompareAndSwap(p, n) {
			break
		}
	}
	time.Sleep(40 * time.Millisecond)
	a.cur.Add(-1)
	return a2a.EvidenceClaim{AssertionStatus: "ok"}, nil
}

// TestConcurrencyCapBackpressure: with a cap of 2, no more than 2 agents run at
// once even when 6 dispatches are launched together; all complete.
func TestConcurrencyCapBackpressure(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)

	var cur, peak atomic.Int32
	reg := NewRegistry()
	reg.Register("tracker", func() Agent { return trackingAgent{cur: &cur, peak: &peak} })
	d := NewDispatcher(reg, s, 5*time.Second, WithMaxConcurrency(2))

	const n = 6
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tid := seedTask(t, s)
			req := a2a.Request{Role: "tracker", Obligation: a2a.ProofObligation{TaskID: tid}}
			if _, err := d.Dispatch(ctx, req, "attempt-1"); err != nil {
				t.Errorf("dispatch %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if got := peak.Load(); got > 2 {
		t.Fatalf("peak concurrency = %d, want <= 2 (cap not enforced)", got)
	}
	if got := peak.Load(); got < 2 {
		t.Fatalf("peak concurrency = %d, expected to reach the cap of 2", got)
	}
}
