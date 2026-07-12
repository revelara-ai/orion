package llmclient

import (
	"context"
	"errors"
)

// ErrShedBackground: a background-class call was shed because the shared
// in-flight cap is full (or-mvr.3). Background work retries later or is
// skipped; it never queues against interactive work.
var ErrShedBackground = errors.New("llmclient: background call shed under load")

// TrafficClass classifies a call for the shared in-flight cap. The zero value
// is interactive, so untagged callers get the strong (waiting) semantics.
type TrafficClass int

const (
	// ClassInteractive: developer-facing work — waits for a slot.
	ClassInteractive TrafficClass = iota
	// ClassBackground: shadow runs, speculative work — shed when the cap is
	// full, FIRST, so first-party background traffic can never starve the
	// interactive path (inc-qdi: internal traffic bypassed the overload
	// controls external traffic was subject to).
	ClassBackground
)

// InflightGate is ONE in-flight ceiling for every model call in the process —
// coordinator inference, dispatched agents, and background/shadow runs all
// draw from the same cap (no bypass lane). Interactive callers wait for a
// slot; background callers are shed immediately when the cap is full.
type InflightGate struct {
	sem chan struct{}
}

// NewInflightGate returns a gate admitting at most cap concurrent calls
// (cap < 1 is coerced to 1).
func NewInflightGate(cap int) *InflightGate {
	if cap < 1 {
		cap = 1
	}
	return &InflightGate{sem: make(chan struct{}, cap)}
}

// Acquire takes a slot, honoring the class policy. It returns a release
// function that MUST be called when the work finishes.
func (g *InflightGate) Acquire(ctx context.Context, class TrafficClass) (func(), error) {
	if class == ClassBackground {
		select {
		case g.sem <- struct{}{}:
			return g.release, nil
		default:
			return nil, ErrShedBackground
		}
	}
	select {
	case g.sem <- struct{}{}:
		return g.release, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (g *InflightGate) release() { <-g.sem }

type inflightGateKey struct{}
type trafficClassKey struct{}

// WithInflightGate scopes the shared in-flight gate to ctx (installed once at
// the operation root, like the retry budget).
func WithInflightGate(ctx context.Context, g *InflightGate) context.Context {
	return context.WithValue(ctx, inflightGateKey{}, g)
}

// GateFrom returns the ctx's gate, or nil (no gating).
func GateFrom(ctx context.Context) *InflightGate {
	g, _ := ctx.Value(inflightGateKey{}).(*InflightGate)
	return g
}

// WithTrafficClass tags all calls beneath ctx with class (background call
// sites opt in; everything else defaults to interactive).
func WithTrafficClass(ctx context.Context, class TrafficClass) context.Context {
	return context.WithValue(ctx, trafficClassKey{}, class)
}

// ClassFrom returns the ctx's traffic class (interactive by default).
func ClassFrom(ctx context.Context) TrafficClass {
	c, _ := ctx.Value(trafficClassKey{}).(TrafficClass)
	return c
}
