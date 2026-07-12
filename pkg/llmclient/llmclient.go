// Package llmclient is the resilient wrapper every external call (LLM provider,
// Polaris) goes through (or-n8j, PRD Harness Reliability Requirements). It gives
// each call a per-call timeout, exponential backoff with full jitter honoring
// Retry-After, and a circuit breaker — so a hung or failing dependency can never
// block the TUI indefinitely or be hammered in a retry storm.
//
// Manifesto: Orion inherits the failure modes it fights, so it eats its own dog
// food at the harness layer.
package llmclient

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"
)

// ErrCircuitOpen is returned when the breaker is open and a call is short-circuited.
var ErrCircuitOpen = errors.New("llmclient: circuit breaker open")

// RetryAfter wraps an error that carries a server-suggested retry delay.
type RetryAfter struct {
	After time.Duration
	Err   error
}

func (e *RetryAfter) Error() string { return fmt.Sprintf("retry after %s: %v", e.After, e.Err) }
func (e *RetryAfter) Unwrap() error { return e.Err }

// Retryable marks an error as worth retrying (e.g. 5xx, network blip). Timeouts
// and RetryAfter are always retryable.
type Retryable struct{ Err error }

func (e *Retryable) Error() string { return e.Err.Error() }
func (e *Retryable) Unwrap() error { return e.Err }

// breaker is a simple closed/open/half-open circuit breaker.
type breaker struct {
	mu        sync.Mutex
	failures  int
	threshold int
	openUntil time.Time
	cooldown  time.Duration
	now       func() time.Time
}

func (b *breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() || !b.now().Before(b.openUntil) {
		return true // closed, or cooldown elapsed → half-open trial
	}
	return false
}

func (b *breaker) record(success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if success {
		b.failures = 0
		b.openUntil = time.Time{}
		return
	}
	b.failures++
	if b.failures >= b.threshold {
		b.openUntil = b.now().Add(b.cooldown)
	}
}

// Config tunes a Client. Zero values get sane defaults.
type Config struct {
	Timeout          time.Duration // per-call wall-clock timeout
	MaxRetries       int           // additional attempts after the first
	BaseBackoff      time.Duration // base for exponential backoff
	MaxBackoff       time.Duration
	FailureThreshold int           // consecutive failures before the breaker opens
	Cooldown         time.Duration // how long the breaker stays open

	// Injectable for deterministic tests; default to wall-clock / real sleep.
	now   func() time.Time
	sleep func(time.Duration)
	rng   *rand.Rand
}

// Client applies timeout, retry, and breaker policy to operations.
type Client struct {
	cfg     Config
	breaker *breaker
}

// New returns a Client with defaults filled in.
func New(cfg Config) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 100 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 10 * time.Second
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 30 * time.Second
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.sleep == nil {
		cfg.sleep = time.Sleep
	}
	if cfg.rng == nil {
		cfg.rng = rand.New(rand.NewSource(1))
	}
	return &Client{
		cfg: cfg,
		breaker: &breaker{
			threshold: cfg.FailureThreshold,
			cooldown:  cfg.Cooldown,
			now:       cfg.now,
		},
	}
}

// Do runs op with the client's timeout, retry, and breaker policy. T is the
// operation's result type.
func Do[T any](ctx context.Context, c *Client, op func(context.Context) (T, error)) (T, error) {
	var zero T
	var lastErr error
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if !c.breaker.allow() {
			return zero, ErrCircuitOpen
		}
		callCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
		v, err := op(callCtx)
		cancel()
		if err == nil {
			c.breaker.record(true)
			return v, nil
		}
		c.breaker.record(false)
		lastErr = err
		if !retryable(err) || attempt == c.cfg.MaxRetries {
			return zero, err
		}
		// Stack-wide retry budget (or-mvr.1): a retry anywhere in the operation
		// draws from ONE ceiling, so total attempts can never multiply across
		// layers. No budget in ctx → per-call semantics unchanged.
		if b := BudgetFrom(ctx); b != nil && !b.Take() {
			return zero, exhausted(b, err)
		}
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		default:
		}
		c.cfg.sleep(c.backoff(attempt, err))
	}
	return zero, lastErr
}

// backoff is exponential with full jitter, honoring Retry-After when present.
func (c *Client) backoff(attempt int, err error) time.Duration {
	var ra *RetryAfter
	if errors.As(err, &ra) && ra.After > 0 {
		return ra.After
	}
	exp := float64(c.cfg.BaseBackoff) * math.Pow(2, float64(attempt))
	if exp > float64(c.cfg.MaxBackoff) {
		exp = float64(c.cfg.MaxBackoff)
	}
	return time.Duration(c.cfg.rng.Int63n(int64(exp) + 1)) // full jitter in [0, exp]
}

func retryable(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ra *RetryAfter
	if errors.As(err, &ra) {
		return true
	}
	var r *Retryable
	return errors.As(err, &r)
}
