package harness

import (
	"bytes"
	"encoding/json"
	"errors"
)

// ErrStalled marks a turn stopped because the model repeated the identical
// tool call past the abort threshold — a loop whose result cannot change
// (observed: a small local model re-issuing the same failing add_case until
// MaxIterations). Distinct from ErrMaxIterations so surfaces can say WHY.
var ErrStalled = errors.New("stalled")

const (
	// stallNudgeAt: consecutive identical calls before the harness stops
	// executing them and returns a corrective error result instead.
	stallNudgeAt = 3
	// stallAbortAt: consecutive identical calls before the turn is stopped.
	stallAbortAt = 5
)

// stallTracker counts consecutive identical (tool, input) dispatches within a
// turn. Input equality is whitespace-insensitive (compacted JSON): the same
// call reformatted is still the same call. Any different call resets it —
// legitimate repeats (re-running go test after an edit) are interleaved with
// other calls and never accumulate.
type stallTracker struct {
	name  string
	input string
	count int
}

// observe records a dispatch and returns the consecutive-identical count
// INCLUDING this call.
func (s *stallTracker) observe(name string, input json.RawMessage) int {
	key := compactJSON(input)
	if name == s.name && key == s.input {
		s.count++
	} else {
		s.name, s.input, s.count = name, key, 1
	}
	return s.count
}

func compactJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}
