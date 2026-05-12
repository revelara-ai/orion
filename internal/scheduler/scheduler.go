package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrSchedulerStopped is returned by Run when the parent ctx is
// cancelled. Callers can errors.Is on this to distinguish a clean
// shutdown from an unexpected error.
var ErrSchedulerStopped = errors.New("scheduler: stopped")

// BindingDescriptor is the per-binding payload Scheduler iterates
// over each polling interval. The scheduler doesn't own the binding
// row; the BindingsSource adapter (production: TrackerBindingRepo
// scan; tests: stub slice) supplies them.
type BindingDescriptor struct {
	BindingID uuid.UUID
	OrgID     uuid.UUID
	RepoID    uuid.UUID
	Mode      CadenceMode
}

// TickFunc is the callback invoked when the cadence layer says fire.
// Production wiring adapts to detection.LoopDriver.Tick. The
// scheduler does not depend on internal/detection directly so the
// scheduler package stays at a lower layer in the dep graph.
type TickFunc func(ctx context.Context, b BindingDescriptor) error

// BindingsFunc returns the current set of bindings to evaluate. The
// scheduler calls this each tick so adds/removes between ticks land
// in the next pass.
type BindingsFunc func(ctx context.Context) ([]BindingDescriptor, error)

// EligibleCounterFunc returns the eligible-backlog count for the
// given org + repo, scoped to the caller's RLS context.
type EligibleCounterFunc func(ctx context.Context, orgID, repoID uuid.UUID) (int, error)

// LastRunFunc returns the most recent detection-run start time for
// the given binding, or zero if it has never run. Adaptive +
// daily/weekly modes consult this.
type LastRunFunc func(ctx context.Context, orgID, bindingID uuid.UUID) (time.Time, error)

// Logger is the minimum logging surface the scheduler needs. The
// stdlib log.Logger satisfies it; a structured logger wraps Printf.
type Logger interface {
	Printf(format string, args ...any)
}

// Scheduler polls bindings on a fixed interval and fires Tick when
// the cadence rules say so. The polling interval is the *scheduler's*
// loop period (default 1 minute), distinct from per-binding cadence.
type Scheduler struct {
	Bindings       BindingsFunc
	Tick           TickFunc
	EligibleCount  EligibleCounterFunc
	LastRun        LastRunFunc
	Logger         Logger
	PollInterval   time.Duration
	BacklogTarget  int
	DailyInterval  time.Duration
	WeeklyInterval time.Duration
	Clock          func() time.Time // overridable for tests
}

// NewScheduler constructs a Scheduler with the supplied collaborators.
// PollInterval defaults to 1m, BacklogTarget to 20.
func NewScheduler(s Scheduler) *Scheduler {
	if s.PollInterval <= 0 {
		s.PollInterval = time.Minute
	}
	if s.BacklogTarget <= 0 {
		s.BacklogTarget = DefaultBacklogTarget
	}
	if s.DailyInterval <= 0 {
		s.DailyInterval = DefaultDailyInterval
	}
	if s.WeeklyInterval <= 0 {
		s.WeeklyInterval = DefaultWeeklyInterval
	}
	if s.Clock == nil {
		s.Clock = time.Now
	}
	return &s
}

// Trigger is the on-demand path: bypass cadence and run Tick for one
// binding immediately. Returns the error from Tick directly. Used by
// `orion-cli detection trigger` and (future) the GitHub on_push
// webhook handler.
func (s *Scheduler) Trigger(ctx context.Context, b BindingDescriptor) error {
	if s.Tick == nil {
		return errors.New("scheduler: Tick callback is nil")
	}
	if s.Logger != nil {
		s.Logger.Printf("scheduler: trigger binding=%s reason=explicit", b.BindingID)
	}
	return s.Tick(ctx, b)
}

// Run polls Bindings on PollInterval, applies the cadence decision
// per binding, and invokes Tick when allowed. Exits when ctx is
// done, returning ErrSchedulerStopped.
func (s *Scheduler) Run(ctx context.Context) error {
	if s.Bindings == nil || s.Tick == nil {
		return errors.New("scheduler: Bindings and Tick callbacks required")
	}

	t := time.NewTicker(s.PollInterval)
	defer t.Stop()

	// Run one pass immediately so the first tick doesn't wait
	// PollInterval after server boot.
	s.runPass(ctx)

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: ctx done", ErrSchedulerStopped)
		case <-t.C:
			s.runPass(ctx)
		}
	}
}

// runPass evaluates each binding once. Errors from one binding don't
// abort the pass; they're logged and the next binding is evaluated.
func (s *Scheduler) runPass(ctx context.Context) {
	bindings, err := s.Bindings(ctx)
	if err != nil {
		if s.Logger != nil {
			s.Logger.Printf("scheduler: bindings: %v", err)
		}
		return
	}
	for _, b := range bindings {
		s.evaluate(ctx, b)
	}
}

// evaluate runs the cadence decision for one binding and fires Tick
// if allowed. Failures are logged but don't propagate; the polling
// loop continues so one slow binding can't starve the others.
func (s *Scheduler) evaluate(ctx context.Context, b BindingDescriptor) {
	now := s.Clock()

	var depth int
	if b.Mode == CadenceAdaptive || b.Mode == "" {
		if s.EligibleCount != nil {
			d, err := s.EligibleCount(ctx, b.OrgID, b.RepoID)
			if err != nil {
				if s.Logger != nil {
					s.Logger.Printf("scheduler: count eligible binding=%s: %v", b.BindingID, err)
				}
				return
			}
			depth = d
		}
	}

	var lastRun time.Time
	if b.Mode != CadenceOnDemand && s.LastRun != nil {
		lr, err := s.LastRun(ctx, b.OrgID, b.BindingID)
		if err != nil {
			if s.Logger != nil {
				s.Logger.Printf("scheduler: last run binding=%s: %v", b.BindingID, err)
			}
			return
		}
		lastRun = lr
	}

	dec := DecideTrigger(DecideInput{
		Mode:           b.Mode,
		Now:            now,
		LastRunAt:      lastRun,
		EligibleDepth:  depth,
		BacklogTarget:  s.BacklogTarget,
		DailyInterval:  s.DailyInterval,
		WeeklyInterval: s.WeeklyInterval,
	})

	if !dec.Fire {
		if s.Logger != nil {
			s.Logger.Printf("scheduler: suppress binding=%s mode=%s reason=%q", b.BindingID, b.Mode, dec.Reason)
		}
		return
	}

	if s.Logger != nil {
		s.Logger.Printf("scheduler: fire binding=%s mode=%s reason=%q", b.BindingID, b.Mode, dec.Reason)
	}
	if err := s.Tick(ctx, b); err != nil {
		if s.Logger != nil {
			s.Logger.Printf("scheduler: tick binding=%s: %v", b.BindingID, err)
		}
	}
}
