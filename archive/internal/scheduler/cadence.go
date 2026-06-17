// Package scheduler implements SPEC §15.1's "When the Detection Loop
// Runs" decision surface. The cadence layer answers a per-binding,
// per-tick question: "given current backlog depth and last-run
// timestamp, should the LoopDriver fire now?"
//
// v1 ships four modes:
//
//   - adaptive (default): fire when eligible-backlog-depth drops
//     below detection.backlog_target_depth. Suppresses otherwise.
//   - on_demand: never fires from cadence; only explicit Trigger().
//   - daily / weekly: fixed cadence regardless of depth.
//
// The on_push mode from the SPEC is intentionally absent here; it
// depends on the GitHub webhook handler (E2), which is not yet wired.
// A future slice will register a handler that calls scheduler.Trigger.
package scheduler

import "time"

// CadenceMode enumerates SPEC §15.1 cadence settings.
type CadenceMode string

// Cadence values.
const (
	CadenceAdaptive CadenceMode = "adaptive"
	CadenceOnDemand CadenceMode = "on_demand"
	CadenceDaily    CadenceMode = "daily"
	CadenceWeekly   CadenceMode = "weekly"
)

// Decision is the cadence layer's verdict for one (binding, now)
// pair. Reason is human-readable and meant for logs + DetectionRun
// notes.
type Decision struct {
	Fire   bool
	Reason string
}

// DecideInput is the per-binding payload the cadence layer reads.
type DecideInput struct {
	Mode           CadenceMode
	Now            time.Time
	LastRunAt      time.Time // zero if never run
	EligibleDepth  int
	BacklogTarget  int // typically detection.backlog_target_depth from config
	DailyInterval  time.Duration
	WeeklyInterval time.Duration
}

// Defaults per SPEC §15.1.
const (
	DefaultBacklogTarget  = 20
	DefaultDailyInterval  = 24 * time.Hour
	DefaultWeeklyInterval = 7 * 24 * time.Hour
)

// DecideTrigger returns Fire=true with a reason when the cadence
// rules permit a Tick now. When Fire=false the Reason captures why
// the gate suppressed (caller logs + persists this for audit).
func DecideTrigger(in DecideInput) Decision {
	target := in.BacklogTarget
	if target <= 0 {
		target = DefaultBacklogTarget
	}

	switch in.Mode {
	case CadenceOnDemand:
		return Decision{Fire: false, Reason: "cadence=on_demand: only Trigger() fires"}

	case CadenceDaily:
		interval := in.DailyInterval
		if interval <= 0 {
			interval = DefaultDailyInterval
		}
		if in.LastRunAt.IsZero() || in.Now.Sub(in.LastRunAt) >= interval {
			return Decision{Fire: true, Reason: "cadence=daily: interval elapsed"}
		}
		return Decision{Fire: false, Reason: "cadence=daily: interval not yet elapsed"}

	case CadenceWeekly:
		interval := in.WeeklyInterval
		if interval <= 0 {
			interval = DefaultWeeklyInterval
		}
		if in.LastRunAt.IsZero() || in.Now.Sub(in.LastRunAt) >= interval {
			return Decision{Fire: true, Reason: "cadence=weekly: interval elapsed"}
		}
		return Decision{Fire: false, Reason: "cadence=weekly: interval not yet elapsed"}

	case CadenceAdaptive, "":
		if in.EligibleDepth < target {
			return Decision{Fire: true, Reason: "cadence=adaptive: backlog below target"}
		}
		return Decision{Fire: false, Reason: "cadence=adaptive: backlog at or above target"}

	default:
		return Decision{Fire: false, Reason: "unknown cadence mode: " + string(in.Mode)}
	}
}
