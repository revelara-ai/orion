package harness

import (
	"errors"
	"fmt"
	"strings"
)

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
	name    string
	count   int
	strikes []streakStrike
}

// streakStrike is one recorded failure in a streak — the evidence the abort
// error carries so a live streak is diagnosable from the session log
// (or-nos3: a bare "failed 6×" left the dogfood diffgen failure opaque).
type streakStrike struct {
	input  string
	result string
}

// strike records a failed (or guard-intercepted) call's evidence. Bounded:
// inputs/results are clipped and only the newest errStreakAbortAt kept.
func (s *errStreakTracker) strike(input, result string) {
	s.strikes = append(s.strikes, streakStrike{input: clipStreak(input, 120), result: clipStreak(result, 200)})
	if len(s.strikes) > errStreakAbortAt {
		s.strikes = s.strikes[len(s.strikes)-errStreakAbortAt:]
	}
}

// detail renders the recorded strikes as numbered evidence lines.
func (s *errStreakTracker) detail() string {
	var b strings.Builder
	for i, st := range s.strikes {
		fmt.Fprintf(&b, "\n  strike %d: input=%s → %s", i+1, st.input, st.result)
	}
	return b.String()
}

func clipStreak(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
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
func (s *errStreakTracker) reset() { s.count, s.strikes = 0, nil }
