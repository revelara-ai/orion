package llmclient

import (
	"context"
	"errors"
	"testing"
	"time"
)

func instantClient(maxRetries int) *Client {
	return New(Config{
		Timeout:          time.Second,
		MaxRetries:       maxRetries,
		FailureThreshold: 1 << 30, // keep the per-client breaker out of this test's way
		sleep:            func(time.Duration) {},
	})
}

// TestRetryBudgetBoundsTotalAttemptsAcrossTheStack is the or-mvr.1 acceptance:
// every layer sees a retriable error; total attempts across ALL calls sharing
// the operation context are bounded by ONE configured budget — never the
// product (or even the sum) of per-layer MaxRetries.
func TestRetryBudgetBoundsTotalAttemptsAcrossTheStack(t *testing.T) {
	attempts := 0
	failing := func(context.Context) (int, error) {
		attempts++
		return 0, &Retryable{Err: errors.New("529 overloaded")}
	}

	const calls, perCallRetries, budget = 10, 5, 3
	ctx := WithRetryBudget(context.Background(), NewRetryBudget(budget))
	layerA, layerB := instantClient(perCallRetries), instantClient(perCallRetries)

	var lastErr error
	for i := 0; i < calls; i++ {
		c := layerA
		if i%2 == 1 {
			c = layerB // two "layers" hammered alternately, same operation ctx
		}
		_, lastErr = Do(ctx, c, failing)
	}

	// Without the budget: calls × (1+retries) = 60 attempts. With it: every call
	// still gets its FIRST attempt (progress is never starved), but the whole
	// operation shares `budget` retries.
	if want := calls + budget; attempts != want {
		t.Fatalf("total attempts = %d, want %d (first attempts %d + shared budget %d); the per-layer product would be %d",
			attempts, want, calls, budget, calls*(1+perCallRetries))
	}
	if !errors.Is(lastErr, ErrRetryBudgetExhausted) {
		t.Fatalf("exhaustion must be named so the caller can escalate, got: %v", lastErr)
	}
}

// TestRetryBudgetSharedAcrossNestedCalls: an op that itself calls Do (proof
// loop inside an agent turn) draws from the SAME ctx budget.
func TestRetryBudgetSharedAcrossNestedCalls(t *testing.T) {
	inner := instantClient(5)
	outer := instantClient(5)
	innerAttempts, outerAttempts := 0, 0

	ctx := WithRetryBudget(context.Background(), NewRetryBudget(2))
	_, _ = Do(ctx, outer, func(ctx context.Context) (int, error) {
		outerAttempts++
		_, _ = Do(ctx, inner, func(context.Context) (int, error) {
			innerAttempts++
			return 0, &Retryable{Err: errors.New("blip")}
		})
		return 0, &Retryable{Err: errors.New("blip")}
	})
	total := innerAttempts + outerAttempts
	// Budget 2 → at most first attempts (outer 1 + one inner per outer attempt)
	// plus 2 shared retries anywhere. The exact split doesn't matter; the bound does.
	if total > 2+2+2 {
		t.Fatalf("nested calls must share one budget: %d total attempts (inner %d, outer %d)", total, innerAttempts, outerAttempts)
	}
}

// TestNoBudgetInContextKeepsPerCallBehavior: absent a budget, Do retries up to
// its own MaxRetries exactly as before (backward compatible).
func TestNoBudgetInContextKeepsPerCallBehavior(t *testing.T) {
	attempts := 0
	_, err := Do(context.Background(), instantClient(4), func(context.Context) (int, error) {
		attempts++
		return 0, &Retryable{Err: errors.New("blip")}
	})
	if attempts != 5 {
		t.Fatalf("without a ctx budget Do must keep per-call semantics: %d attempts, want 5", attempts)
	}
	if errors.Is(err, ErrRetryBudgetExhausted) {
		t.Fatal("no budget error without a budget")
	}
}

// TestRetryBudgetDoesNotStarveFirstAttempts: an exhausted budget still lets a
// NEW call make its first attempt — the budget bounds RETRIES, not work.
func TestRetryBudgetDoesNotStarveFirstAttempts(t *testing.T) {
	b := NewRetryBudget(0)
	ctx := WithRetryBudget(context.Background(), b)
	ok := false
	_, err := Do(ctx, instantClient(5), func(context.Context) (bool, error) {
		ok = true
		return true, nil
	})
	if err != nil || !ok {
		t.Fatalf("first attempt must run on a zero budget: ok=%v err=%v", ok, err)
	}
}
