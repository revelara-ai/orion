package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ProgressEvent is an incremental liveness signal from a long operation (per-mode
// start/finish, mutation %, Lookout boot/probe, heartbeat). Understanding is a
// first-class output: no pane goes silent for long (or-78r, PRD TUI liveness).
type ProgressEvent struct {
	Phase     string
	Detail    string
	Actor     string
	Depth     int
	Status    string
	Heartbeat bool
	At        time.Time
}

// ProgressBus collects progress events and enforces a liveness heartbeat: if no
// event arrives within the heartbeat interval, a heartbeat tick is emitted so the
// TUI never appears hung.
type ProgressBus struct {
	mu        sync.Mutex
	events    []ProgressEvent
	last      time.Time
	heartbeat time.Duration
	now       func() time.Time
}

// NewProgressBus returns a bus with the given heartbeat interval.
func NewProgressBus(heartbeat time.Duration) *ProgressBus {
	now := time.Now
	return &ProgressBus{heartbeat: heartbeat, now: now, last: now()}
}

// Emit records a progress event and resets the heartbeat window.
func (b *ProgressBus) Emit(phase, detail string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := b.now()
	b.events = append(b.events, ProgressEvent{Phase: phase, Detail: detail, At: t})
	b.last = t
}

// EmitActivity records a who-is-doing-what event (actor + activity + call-stack
// depth + status). Resets the heartbeat window like Emit.
func (b *ProgressBus) EmitActivity(actor, activity string, depth int, status string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := b.now()
	b.events = append(b.events, ProgressEvent{
		Phase: activity, Detail: activity, Actor: actor, Depth: depth, Status: status, At: t,
	})
	b.last = t
}

// HeartbeatDue reports whether the heartbeat window has elapsed without an event.
func (b *ProgressBus) HeartbeatDue(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return now.Sub(b.last) >= b.heartbeat
}

// Tick emits a heartbeat event if one is due, so no pane stays silent past the
// interval. Returns true if a heartbeat was emitted.
func (b *ProgressBus) Tick(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if now.Sub(b.last) < b.heartbeat {
		return false
	}
	b.events = append(b.events, ProgressEvent{Phase: "heartbeat", Detail: "…working", Heartbeat: true, At: now})
	b.last = now
	return true
}

// nowForTest exposes the bus clock for unit tests that need to fabricate a future time.
func (b *ProgressBus) nowForTest() time.Time { return b.now() }

// Events returns a snapshot of all events.
func (b *ProgressBus) Events() []ProgressEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]ProgressEvent, len(b.events))
	copy(out, b.events)
	return out
}

// MaxSilence returns the longest gap between consecutive events (incl. now),
// used to assert no pane went silent too long.
func (b *ProgressBus) MaxSilence(now time.Time) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.events) == 0 {
		return now.Sub(b.last)
	}
	maxGap := time.Duration(0)
	prev := b.events[0].At
	for _, e := range b.events[1:] {
		if g := e.At.Sub(prev); g > maxGap {
			maxGap = g
		}
		prev = e.At
	}
	if g := now.Sub(prev); g > maxGap {
		maxGap = g
	}
	return maxGap
}

// RenderProgress renders the proof/fleet progress for a pane.
func RenderProgress(events []ProgressEvent) string {
	var b strings.Builder
	b.WriteString("# Progress\n")
	for _, e := range events {
		mark := "·"
		if e.Heartbeat {
			mark = "♥"
		}
		b.WriteString(fmt.Sprintf("%s [%s] %s\n", mark, e.Phase, e.Detail))
	}
	return b.String()
}
