package harness

import "errors"

// ErrErrorStreak marks a turn stopped because ONE tool kept returning errors
// past the abort threshold — with VARYING inputs, so the identical-input stall
// detector never fires (observed live: a model grinding add_case grounding
// failures through the whole iteration budget). Distinct from ErrStalled and
// ErrMaxIterations so surfaces can say WHY.
var ErrErrorStreak = errors.New("error streak")

const (
	// errStreakNudgeAt: consecutive error results from the same tool before
	// the harness stops executing it and returns a corrective error result
	// instead.
	errStreakNudgeAt = 4
	// errStreakAbortAt: consecutive error results from the same tool before
	// the turn is stopped.
	errStreakAbortAt = 6
)

// errStreakTracker counts consecutive ERROR results from the same tool (ANY
// input) within a turn. A success from that tool, or a call to any different
// tool, resets it — legitimate error-then-investigate cycles (a failing test
// between read_file calls) are interleaved with other tools and never
// accumulate. Complements stallTracker, which needs the input to be IDENTICAL.
type errStreakTracker struct {
	name  string
	count int
}

// observe records a call to name and returns the consecutive same-tool error
// count INCLUDING this call, assuming it fails; a call that then succeeds is
// forgiven via reset. A synthetic guard result IS an error result from the
// model's perspective, so intercepted calls stay counted.
func (s *errStreakTracker) observe(name string) int {
	if name == s.name {
		s.count++
	} else {
		s.name, s.count = name, 1
	}
	return s.count
}

// reset clears the streak after the tool succeeded — the approach works.
func (s *errStreakTracker) reset() { s.count = 0 }
