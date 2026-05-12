package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDecideTrigger_Adaptive_FiresWhenBelowTarget(t *testing.T) {
	dec := DecideTrigger(DecideInput{
		Mode:          CadenceAdaptive,
		EligibleDepth: 5,
		BacklogTarget: 20,
	})
	if !dec.Fire {
		t.Errorf("expected fire with depth=5 < target=20; got %+v", dec)
	}
}

func TestDecideTrigger_Adaptive_SuppressesAtOrAboveTarget(t *testing.T) {
	for _, depth := range []int{20, 21, 50} {
		dec := DecideTrigger(DecideInput{
			Mode:          CadenceAdaptive,
			EligibleDepth: depth,
			BacklogTarget: 20,
		})
		if dec.Fire {
			t.Errorf("depth=%d should suppress; got %+v", depth, dec)
		}
	}
}

func TestDecideTrigger_OnDemand_NeverFires(t *testing.T) {
	dec := DecideTrigger(DecideInput{
		Mode:          CadenceOnDemand,
		EligibleDepth: 0,
		BacklogTarget: 20,
	})
	if dec.Fire {
		t.Errorf("on_demand should never fire from cadence; got %+v", dec)
	}
}

func TestDecideTrigger_Daily(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	// Never run before → fire
	dec := DecideTrigger(DecideInput{Mode: CadenceDaily, Now: now})
	if !dec.Fire {
		t.Errorf("never-run daily should fire; got %+v", dec)
	}

	// 25h ago → fire
	dec = DecideTrigger(DecideInput{Mode: CadenceDaily, Now: now, LastRunAt: now.Add(-25 * time.Hour)})
	if !dec.Fire {
		t.Errorf("25h-old daily should fire; got %+v", dec)
	}

	// 23h ago → suppress
	dec = DecideTrigger(DecideInput{Mode: CadenceDaily, Now: now, LastRunAt: now.Add(-23 * time.Hour)})
	if dec.Fire {
		t.Errorf("23h-old daily should suppress; got %+v", dec)
	}
}

func TestDecideTrigger_Weekly(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	dec := DecideTrigger(DecideInput{Mode: CadenceWeekly, Now: now, LastRunAt: now.Add(-8 * 24 * time.Hour)})
	if !dec.Fire {
		t.Errorf("8d-old weekly should fire; got %+v", dec)
	}
	dec = DecideTrigger(DecideInput{Mode: CadenceWeekly, Now: now, LastRunAt: now.Add(-6 * 24 * time.Hour)})
	if dec.Fire {
		t.Errorf("6d-old weekly should suppress; got %+v", dec)
	}
}

func TestDecideTrigger_UnknownMode(t *testing.T) {
	dec := DecideTrigger(DecideInput{Mode: "garbage"})
	if dec.Fire {
		t.Errorf("unknown mode should suppress; got %+v", dec)
	}
}

// Scheduler.Run + per-binding evaluation tests.

type stubLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *stubLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, format)
}

func TestScheduler_Trigger_BypassesCadence(t *testing.T) {
	called := 0
	s := NewScheduler(Scheduler{
		Tick: func(ctx context.Context, b BindingDescriptor) error {
			called++
			return nil
		},
	})

	b := BindingDescriptor{BindingID: uuid.New(), Mode: CadenceOnDemand}
	if err := s.Trigger(context.Background(), b); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if called != 1 {
		t.Errorf("Trigger should call Tick once; got %d", called)
	}
}

func TestScheduler_Run_AdaptiveFiresThenSuppresses(t *testing.T) {
	binding := BindingDescriptor{
		BindingID: uuid.New(),
		OrgID:     uuid.New(),
		RepoID:    uuid.New(),
		Mode:      CadenceAdaptive,
	}

	// Adaptive: depth=5 < target=20 → first pass fires; raise depth
	// to 50 so the second pass suppresses.
	depth := 5
	var depthMu sync.Mutex

	tickCount := 0
	var tickMu sync.Mutex

	s := NewScheduler(Scheduler{
		Bindings: func(ctx context.Context) ([]BindingDescriptor, error) {
			return []BindingDescriptor{binding}, nil
		},
		EligibleCount: func(ctx context.Context, _, _ uuid.UUID) (int, error) {
			depthMu.Lock()
			defer depthMu.Unlock()
			return depth, nil
		},
		LastRun: func(ctx context.Context, _, _ uuid.UUID) (time.Time, error) {
			return time.Time{}, nil
		},
		Tick: func(ctx context.Context, b BindingDescriptor) error {
			tickMu.Lock()
			tickCount++
			tickMu.Unlock()
			return nil
		},
		Logger:        &stubLogger{},
		PollInterval:  10 * time.Millisecond,
		BacklogTarget: 20,
	})

	// First pass: should fire.
	s.evaluate(context.Background(), binding)
	tickMu.Lock()
	first := tickCount
	tickMu.Unlock()
	if first != 1 {
		t.Errorf("after pass 1 (depth=5): tickCount=%d, want 1", first)
	}

	// Raise depth above target; next evaluate should suppress.
	depthMu.Lock()
	depth = 50
	depthMu.Unlock()
	s.evaluate(context.Background(), binding)
	tickMu.Lock()
	second := tickCount
	tickMu.Unlock()
	if second != 1 {
		t.Errorf("after pass 2 (depth=50): tickCount=%d, want still 1 (suppressed)", second)
	}
}

func TestScheduler_Run_ExitsOnContextCancel(t *testing.T) {
	s := NewScheduler(Scheduler{
		Bindings: func(ctx context.Context) ([]BindingDescriptor, error) {
			return nil, nil
		},
		Tick:         func(ctx context.Context, b BindingDescriptor) error { return nil },
		PollInterval: 10 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- s.Run(ctx) }()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrSchedulerStopped) {
			t.Errorf("Run returned %v, want ErrSchedulerStopped", err)
		}
	case <-time.After(time.Second):
		t.Error("Run did not exit after ctx cancel")
	}
}

func TestScheduler_Run_RequiresCallbacks(t *testing.T) {
	s := NewScheduler(Scheduler{})
	err := s.Run(context.Background())
	if err == nil {
		t.Error("Run with no callbacks should error")
	}
}

func TestScheduler_evaluate_DailyMode(t *testing.T) {
	binding := BindingDescriptor{
		BindingID: uuid.New(),
		OrgID:     uuid.New(),
		RepoID:    uuid.New(),
		Mode:      CadenceDaily,
	}
	fixedNow := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	tickCount := 0

	// LastRun > 24h ago: should fire
	s := NewScheduler(Scheduler{
		Bindings:      func(ctx context.Context) ([]BindingDescriptor, error) { return []BindingDescriptor{binding}, nil },
		EligibleCount: func(ctx context.Context, _, _ uuid.UUID) (int, error) { return 9999, nil },
		LastRun: func(ctx context.Context, _, _ uuid.UUID) (time.Time, error) {
			return fixedNow.Add(-25 * time.Hour), nil
		},
		Tick:  func(ctx context.Context, b BindingDescriptor) error { tickCount++; return nil },
		Clock: func() time.Time { return fixedNow },
	})
	s.evaluate(context.Background(), binding)
	if tickCount != 1 {
		t.Errorf("daily 25h: tickCount=%d, want 1", tickCount)
	}
}
