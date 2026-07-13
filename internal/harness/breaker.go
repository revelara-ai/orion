package harness

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/revelara-ai/orion/pkg/llm"
)

// ErrProvider marks a turn that failed because the model PROVIDER failed —
// the dependency, not the work. Loop.Run wraps provider errors with it so
// long-lived callers can classify turn failures (the Breaker counts only
// these; a proof Reject or a user cancel is not a dead dependency).
var ErrProvider = errors.New("harness: provider error")

// ErrBreakerOpen reports the loop-level circuit breaker open — N consecutive
// turns failed on the provider, so the loop escalates instead of burning
// budget on a dead dependency (or-mvr.2; the C4 failure mode of inc-qdi).
var ErrBreakerOpen = errors.New("harness: loop circuit breaker open")

const (
	// defaultBreakerThreshold: consecutive provider-failed turns before the
	// breaker opens. Three failed turns is a degraded dependency, not noise —
	// each turn already survived llmclient's per-call retries.
	defaultBreakerThreshold = 3
	// defaultHalfOpenAfter: how long the breaker refuses turns before letting
	// ONE probe through to test recovery.
	defaultHalfOpenAfter = time.Minute
)

// Breaker is the failure-ACCUMULATING loop-level circuit breaker (or-mvr.2):
// it counts consecutive provider-class turn failures across a long-lived loop
// (a session serving turn after turn) and opens at Threshold — the HTTP-client
// breaker protects one call; this protects the loop above it. One good turn
// closes it. While open, Allow refuses fast; after HalfOpenAfter it admits a
// single probe turn — a failed probe re-opens with a fresh timer, a successful
// one closes the breaker. The zero value is usable.
type Breaker struct {
	Threshold     int               // consecutive provider-failed turns to open; <=0 → defaultBreakerThreshold
	HalfOpenAfter time.Duration     // cool-down before a probe turn; <=0 → defaultHalfOpenAfter
	OnOpen        func(cause error) // optional escalation hook, fired exactly once per open transition

	mu          sync.Mutex
	consecutive int
	open        bool
	openedAt    time.Time
	probing     bool // a half-open probe is in flight
	cause       error
	now         func() time.Time // test seam; nil → time.Now
}

func (b *Breaker) threshold() int {
	if b.Threshold > 0 {
		return b.Threshold
	}
	return defaultBreakerThreshold
}

func (b *Breaker) halfOpenAfter() time.Duration {
	if b.HalfOpenAfter > 0 {
		return b.HalfOpenAfter
	}
	return defaultHalfOpenAfter
}

func (b *Breaker) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// Allow reports whether the loop may run a turn. When the breaker is open it
// returns ErrBreakerOpen (with the accumulated cause) — except that once per
// HalfOpenAfter window a single probe turn is admitted to test recovery.
func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.open {
		return nil
	}
	if !b.probing && b.clock().Sub(b.openedAt) >= b.halfOpenAfter() {
		b.probing = true // one probe per window
		return nil
	}
	return fmt.Errorf("%w after %d consecutive provider failures (%v) — waiting for the provider to recover; a probe turn runs every %s, or switch models", ErrBreakerOpen, b.threshold(), b.cause, b.halfOpenAfter())
}

// Observe records a finished turn's outcome. Only provider-class failures
// accumulate (ErrProvider, excluding context overflow — a sizing problem —
// and context cancellation — a user action); anything else is a working
// dependency and closes/resets the breaker.
func (b *Breaker) Observe(err error) {
	bad := err != nil &&
		errors.Is(err, ErrProvider) &&
		!errors.Is(err, llm.ErrContextOverflow) &&
		!errors.Is(err, context.Canceled) &&
		!errors.Is(err, context.DeadlineExceeded)
	b.mu.Lock()
	defer b.mu.Unlock()
	if !bad {
		if b.open || b.consecutive > 0 {
			slog.Info("loop breaker closed", "after_consecutive_failures", b.consecutive)
		}
		b.open, b.probing, b.consecutive, b.cause = false, false, 0, nil
		return
	}
	b.consecutive++
	b.cause = err
	if b.open {
		// A failed half-open probe: re-open with a fresh cool-down window.
		b.openedAt, b.probing = b.clock(), false
		slog.Warn("loop breaker probe failed — staying open", "cause", err)
		return
	}
	if b.consecutive >= b.threshold() {
		b.open, b.openedAt, b.probing = true, b.clock(), false
		slog.Warn("loop breaker OPEN — escalating instead of burning budget on a degraded provider",
			"consecutive_failures", b.consecutive, "threshold", b.threshold(), "cause", err)
		if b.OnOpen != nil {
			b.OnOpen(err)
		}
	}
}

// Open reports whether the breaker is currently open (read-only — unlike
// Allow it never admits a probe).
func (b *Breaker) Open() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.open
}

// Reset closes the breaker unconditionally (developer acknowledgment — e.g. a
// /model switch replaces the dependency, so the accumulated evidence is stale).
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.open, b.probing, b.consecutive, b.cause = false, false, 0, nil
}
