package harness

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/revelara-ai/orion/pkg/llm"
)

func provErr() error { return fmt.Errorf("%w: %v", ErrProvider, errors.New("529 overloaded")) }

func TestBreakerOpensAfterThresholdConsecutiveProviderFailures(t *testing.T) {
	opened := 0
	b := Breaker{Threshold: 3, OnOpen: func(error) { opened++ }}
	for i := 0; i < 2; i++ {
		b.Observe(provErr())
		if err := b.Allow(); err != nil {
			t.Fatalf("breaker must stay closed below threshold (after %d): %v", i+1, err)
		}
	}
	b.Observe(provErr())
	if err := b.Allow(); !errors.Is(err, ErrBreakerOpen) {
		t.Fatalf("breaker must open at threshold, got %v", err)
	}
	if opened != 1 {
		t.Fatalf("OnOpen must fire exactly once at open, got %d", opened)
	}
	// Cause is carried for the human.
	if err := b.Allow(); err == nil || !errors.Is(err, ErrBreakerOpen) {
		t.Fatalf("breaker must stay open, got %v", err)
	}
}

func TestBreakerGoodTurnResetsTheStreak(t *testing.T) {
	b := Breaker{Threshold: 2}
	b.Observe(provErr())
	b.Observe(nil) // a good turn — the dependency works
	b.Observe(provErr())
	if err := b.Allow(); err != nil {
		t.Fatalf("streak must reset on a good turn; breaker opened early: %v", err)
	}
}

func TestBreakerIgnoresNonProviderFailures(t *testing.T) {
	b := Breaker{Threshold: 1}
	b.Observe(errors.New("proof rejected the artifact"))                 // not the dependency's fault
	b.Observe(context.Canceled)                                          // user interrupt
	b.Observe(fmt.Errorf("%w: %w", ErrProvider, llm.ErrContextOverflow)) // overflow is a sizing problem, not a dead dependency
	if err := b.Allow(); err != nil {
		t.Fatalf("non-provider failures must not trip the breaker: %v", err)
	}
}

func TestBreakerHalfOpenProbesAndRecloses(t *testing.T) {
	now := time.Unix(1000, 0)
	b := Breaker{Threshold: 1, HalfOpenAfter: time.Minute, now: func() time.Time { return now }}
	b.Observe(provErr())
	if err := b.Allow(); !errors.Is(err, ErrBreakerOpen) {
		t.Fatal("breaker must be open")
	}
	// Before the cool-down: still refused.
	now = now.Add(30 * time.Second)
	if err := b.Allow(); !errors.Is(err, ErrBreakerOpen) {
		t.Fatal("must refuse before the half-open cool-down")
	}
	// After the cool-down: exactly ONE probe is allowed.
	now = now.Add(31 * time.Second)
	if err := b.Allow(); err != nil {
		t.Fatalf("half-open must allow one probe: %v", err)
	}
	if err := b.Allow(); !errors.Is(err, ErrBreakerOpen) {
		t.Fatal("only one probe per cool-down window")
	}
	// Probe fails → re-open, fresh timer.
	b.Observe(provErr())
	now = now.Add(59 * time.Second)
	if err := b.Allow(); !errors.Is(err, ErrBreakerOpen) {
		t.Fatal("failed probe must re-open with a fresh timer")
	}
	// Probe succeeds → fully closed.
	now = now.Add(2 * time.Minute)
	if err := b.Allow(); err != nil {
		t.Fatalf("probe slot: %v", err)
	}
	b.Observe(nil)
	if err := b.Allow(); err != nil {
		t.Fatalf("successful probe must close the breaker: %v", err)
	}
	if err := b.Allow(); err != nil {
		t.Fatalf("closed breaker allows freely: %v", err)
	}
}

func TestBreakerResetCloses(t *testing.T) {
	b := Breaker{Threshold: 1}
	b.Observe(provErr())
	b.Reset()
	if err := b.Allow(); err != nil {
		t.Fatalf("Reset must close the breaker: %v", err)
	}
}

func TestBreakerDefaultThreshold(t *testing.T) {
	var b Breaker // zero value usable
	for i := 0; i < defaultBreakerThreshold-1; i++ {
		b.Observe(provErr())
	}
	if err := b.Allow(); err != nil {
		t.Fatalf("must stay closed below the default threshold: %v", err)
	}
	b.Observe(provErr())
	if err := b.Allow(); !errors.Is(err, ErrBreakerOpen) {
		t.Fatal("must open at the default threshold")
	}
}
