package llmclient

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestInflightGateOneCapBothClasses (or-mvr.3 acceptance, half 1): background
// and interactive draw from the SAME cap — background holding slots blocks
// interactive admission until released (one ceiling, no bypass lane).
func TestInflightGateOneCapBothClasses(t *testing.T) {
	g := NewInflightGate(2)
	rel1, err := g.Acquire(context.Background(), ClassBackground)
	if err != nil {
		t.Fatalf("free slot must admit background: %v", err)
	}
	rel2, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatalf("interactive: %v", err)
	}
	// Cap reached (1 background + 1 interactive): interactive must WAIT, and
	// releasing the background slot must admit it — proof they share the cap.
	admitted := make(chan struct{})
	go func() {
		rel, aerr := g.Acquire(context.Background(), ClassInteractive)
		if aerr == nil {
			defer rel()
			close(admitted)
		}
	}()
	select {
	case <-admitted:
		t.Fatal("interactive must wait while the shared cap is full")
	case <-time.After(50 * time.Millisecond):
	}
	rel1()
	select {
	case <-admitted:
	case <-time.After(2 * time.Second):
		t.Fatal("releasing a background slot must admit the waiting interactive call — the classes must share ONE cap")
	}
	rel2()
}

// TestInflightGateShedsBackgroundFirst (or-mvr.3 acceptance, half 2): under
// pressure (cap full) a background call is SHED immediately with a named
// error — it never queues ahead of (or beside) interactive work.
func TestInflightGateShedsBackgroundFirst(t *testing.T) {
	g := NewInflightGate(1)
	rel, err := g.Acquire(context.Background(), ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	if _, err := g.Acquire(context.Background(), ClassBackground); !errors.Is(err, ErrShedBackground) {
		t.Fatalf("background must shed under pressure, got %v", err)
	}
}

func TestInflightGateInteractiveHonorsCancellation(t *testing.T) {
	g := NewInflightGate(1)
	rel, _ := g.Acquire(context.Background(), ClassInteractive)
	defer rel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := g.Acquire(ctx, ClassInteractive); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting interactive must honor ctx, got %v", err)
	}
}

// TestDoDrawsFromInflightGate: every llmclient.Do call holds a gate slot for
// its whole call (retries included), classed from ctx — coordinator inference,
// dispatched agents, and shadow runs all pass through Do, so this IS the
// single choke point.
func TestDoDrawsFromInflightGate(t *testing.T) {
	g := NewInflightGate(1)
	base := WithInflightGate(context.Background(), g)

	hold := make(chan struct{})
	started := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = Do(base, instantClient(0), func(context.Context) (int, error) {
			close(started)
			<-hold
			return 1, nil
		})
	}()
	<-started // the interactive call occupies the single slot

	// A background call through Do must be shed while the slot is held...
	bg := WithTrafficClass(base, ClassBackground)
	if _, err := Do(bg, instantClient(3), func(context.Context) (int, error) { return 0, nil }); !errors.Is(err, ErrShedBackground) {
		t.Fatalf("background Do must shed under pressure, got %v", err)
	}
	close(hold)
	wg.Wait()
	// ...and admitted when free.
	if _, err := Do(bg, instantClient(0), func(context.Context) (int, error) { return 1, nil }); err != nil {
		t.Fatalf("background Do must run when the cap has room: %v", err)
	}
}

func TestDoWithoutGateUnchanged(t *testing.T) {
	if _, err := Do(context.Background(), instantClient(0), func(context.Context) (int, error) { return 1, nil }); err != nil {
		t.Fatalf("no gate in ctx must mean no gating: %v", err)
	}
}
