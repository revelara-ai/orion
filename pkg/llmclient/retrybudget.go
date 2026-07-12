package llmclient

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrRetryBudgetExhausted: the operation's stack-wide retry budget is spent —
// further failures fail FAST everywhere in the stack instead of amplifying
// (or-mvr.1, the inc-qdi C1 failure mode: one request became 48 retries
// because every layer retried independently).
var ErrRetryBudgetExhausted = errors.New("llmclient: stack-wide retry budget exhausted")

// RetryBudget is ONE anti-amplification ceiling shared by every Do call in an
// operation (a developer turn, a build run, a change run): each retry anywhere
// in the stack draws from it. First attempts are never charged — the budget
// bounds RE-tries, so exhaustion degrades to fail-fast, never to no-work.
type RetryBudget struct {
	mu        sync.Mutex
	remaining int
	spent     int
}

// NewRetryBudget returns a budget of n retries (n <= 0 means no retries beyond
// first attempts).
func NewRetryBudget(n int) *RetryBudget {
	if n < 0 {
		n = 0
	}
	return &RetryBudget{remaining: n}
}

// Take consumes one retry from the budget; false when exhausted.
func (b *RetryBudget) Take() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.remaining <= 0 {
		return false
	}
	b.remaining--
	b.spent++
	return true
}

// Spent reports how many retries the operation has consumed.
func (b *RetryBudget) Spent() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spent
}

type retryBudgetKey struct{}

// WithRetryBudget scopes a stack-wide retry budget to ctx. Install it ONCE at
// the operation root (turn / run); every Do beneath shares it via context.
func WithRetryBudget(ctx context.Context, b *RetryBudget) context.Context {
	return context.WithValue(ctx, retryBudgetKey{}, b)
}

// BudgetFrom returns the ctx's retry budget, or nil when none is installed
// (per-call MaxRetries semantics apply unchanged).
func BudgetFrom(ctx context.Context) *RetryBudget {
	b, _ := ctx.Value(retryBudgetKey{}).(*RetryBudget)
	return b
}

// exhausted wraps err with the named budget error, keeping the cause visible.
func exhausted(b *RetryBudget, err error) error {
	return fmt.Errorf("%w after %d retries: %w", ErrRetryBudgetExhausted, b.Spent(), err)
}
