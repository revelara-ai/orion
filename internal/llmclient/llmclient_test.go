package llmclient

import (
	"context"
	"errors"
	"math/rand"
	"testing"
	"time"
)

// TestLLMCallHasTimeoutAndCircuitBreaker proves both reliability primitives: a
// hung call is bounded by a per-call timeout, and after repeated failures the
// breaker opens and short-circuits without invoking the operation.
func TestLLMCallHasTimeoutAndCircuitBreaker(t *testing.T) {
	t.Run("per-call timeout bounds a hung call", func(t *testing.T) {
		c := New(Config{Timeout: 50 * time.Millisecond, MaxRetries: 0})
		start := time.Now()
		_, err := Do(context.Background(), c, func(ctx context.Context) (int, error) {
			<-ctx.Done() // hang until the per-call timeout fires
			return 0, ctx.Err()
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("want DeadlineExceeded, got %v", err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("call took %s; timeout not enforced", d)
		}
	})

	t.Run("circuit breaker opens and short-circuits", func(t *testing.T) {
		calls := 0
		c := New(Config{
			Timeout:          time.Second,
			MaxRetries:       0,
			FailureThreshold: 3,
			Cooldown:         time.Minute,
			now:              func() time.Time { return time.Unix(0, 0) },
			sleep:            func(time.Duration) {},
			rng:              rand.New(rand.NewSource(1)),
		})
		failing := func(context.Context) (int, error) {
			calls++
			return 0, errors.New("boom")
		}
		// Three failures trip the breaker.
		for i := 0; i < 3; i++ {
			if _, err := Do(context.Background(), c, failing); err == nil {
				t.Fatalf("call %d should have failed", i)
			}
		}
		if calls != 3 {
			t.Fatalf("expected 3 op invocations before open, got %d", calls)
		}
		// Now the breaker is open: the op must NOT be invoked.
		_, err := Do(context.Background(), c, failing)
		if !errors.Is(err, ErrCircuitOpen) {
			t.Fatalf("want ErrCircuitOpen, got %v", err)
		}
		if calls != 3 {
			t.Fatalf("breaker did not short-circuit: op invoked %d times", calls)
		}
	})
}

// TestRetryWithBackoffHonorsRetryAfter: a retryable error is retried up to
// MaxRetries, and Retry-After is honored.
func TestRetryWithBackoffHonorsRetryAfter(t *testing.T) {
	var slept []time.Duration
	c := New(Config{
		Timeout:    time.Second,
		MaxRetries: 2,
		now:        func() time.Time { return time.Unix(0, 0) },
		sleep:      func(d time.Duration) { slept = append(slept, d) },
		rng:        rand.New(rand.NewSource(1)),
	})
	attempts := 0
	v, err := Do(context.Background(), c, func(context.Context) (string, error) {
		attempts++
		if attempts < 3 {
			return "", &RetryAfter{After: 250 * time.Millisecond, Err: errors.New("429")}
		}
		return "ok", nil
	})
	if err != nil || v != "ok" {
		t.Fatalf("expected success after retries, got %q err=%v", v, err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	for _, d := range slept {
		if d != 250*time.Millisecond {
			t.Fatalf("Retry-After not honored; slept %v", slept)
		}
	}
}

// TestNonRetryableErrorNotRetried.
func TestNonRetryableErrorNotRetried(t *testing.T) {
	c := New(Config{Timeout: time.Second, MaxRetries: 5, sleep: func(time.Duration) {}})
	attempts := 0
	_, err := Do(context.Background(), c, func(context.Context) (int, error) {
		attempts++
		return 0, errors.New("permanent")
	})
	if err == nil || attempts != 1 {
		t.Fatalf("non-retryable error should not retry; attempts=%d err=%v", attempts, err)
	}
}
