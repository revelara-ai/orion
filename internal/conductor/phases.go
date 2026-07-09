package conductor

import (
	"github.com/revelara-ai/orion/internal/acp"

	"fmt"
	"strings"
	"sync"
)

// PhaseStatus is the outcome glyph of a build phase.
type PhaseStatus string

const (
	PhaseRunning PhaseStatus = "running" // started, not yet finished
	PhaseDone    PhaseStatus = "done"    // completed green
	PhaseWarn    PhaseStatus = "warn"    // completed, but not green (Reject / escalate / misaligned)
	PhaseFailed  PhaseStatus = "failed"  // errored
)

// PhaseEvent is one structured progress signal from the build pipeline (V3 Step 0).
// The pipeline emits these instead of free text so any surface — the TUI build-report
// card, the CLI, a future per-module dashboard — renders the same phase sequence.
type PhaseEvent struct {
	Phase  string
	Status PhaseStatus
	Detail string
}

// PhaseSink receives build phase events (nil-safe via emit).
type PhaseSink func(PhaseEvent)

func (s PhaseSink) emit(phase string, status PhaseStatus, detail string) {
	if s != nil {
		s(PhaseEvent{Phase: phase, Status: status, Detail: detail})
	}
}

// syncSink wraps a PhaseSink so concurrent emits (from parallel cluster builds, or-tcs.1.4) are
// serialized — the underlying sink (TUI/CLI) need not be thread-safe. nil in → nil out.
func syncSink(s PhaseSink) PhaseSink {
	if s == nil {
		return nil
	}
	var mu sync.Mutex
	return func(e PhaseEvent) {
		mu.Lock()
		defer mu.Unlock()
		s(e)
	}
}

func glyph(s PhaseStatus) string {
	switch s {
	case PhaseDone:
		return "✓"
	case PhaseWarn:
		return "⚠"
	case PhaseFailed:
		return "✗"
	default:
		return "·"
	}
}

// RenderPhaseReport renders the terminal state of each phase as a clean, ordered
// checklist (the body of the TUI build-report card / the CLI summary). Only the
// last status seen per phase is shown, in first-seen order.
func RenderPhaseReport(events []PhaseEvent) string {
	var order []string
	last := map[string]PhaseEvent{}
	for _, e := range events {
		if _, seen := last[e.Phase]; !seen {
			order = append(order, e.Phase)
		}
		last[e.Phase] = e
	}
	var b strings.Builder
	for _, name := range order {
		e := last[name]
		fmt.Fprintf(&b, "%s %s", glyph(e.Status), e.Phase)
		if e.Detail != "" {
			fmt.Fprintf(&b, " — %s", e.Detail)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// phaseActivitySink adapts PhaseEvents to the ACP activity feed — the same
// Phase→activity mapping build_service uses, but carrying Detail so the
// regression gate's per-package heartbeat is visible in the TUI activity panel
// (or-m45w). nil emit → nil sink (phase events are simply dropped).
func phaseActivitySink(emit func(acp.Update)) PhaseSink {
	if emit == nil {
		return nil
	}
	return func(e PhaseEvent) {
		activity := e.Phase
		if e.Detail != "" {
			activity += " — " + e.Detail
		}
		if len(activity) > 120 {
			activity = activity[:119] + "…"
		}
		emit(acp.Activity("Orion", activity, 0, phaseStatusToActivity(e.Status)))
	}
}
