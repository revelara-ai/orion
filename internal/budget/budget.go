// Package budget is Orion's resource-and-cost accountant (or-b6h, PRD Resource &
// Cost Governance). It ALWAYS tracks and surfaces live spend (tokens, dollars,
// wall-clock); hard ceilings are OPT-IN (unset by default). When a ceiling is
// set, crossing ~80% escalates (warn) and crossing 100% halts the run.
//
// Manifesto: reliability is calibrated to the project, not maximized blindly —
// accounting is always on, but the ceiling is the developer's choice.
package budget

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// WarnFraction is the ceiling fraction at which the accountant escalates a warning.
const WarnFraction = 0.8

// State is the budget posture relative to a configured ceiling.
type State string

const (
	StateOK   State = "ok"
	StateWarn State = "warn"
	StateHalt State = "halt"
)

// Ceiling is an opt-in spend limit. A zero field means "no limit on that axis".
type Ceiling struct {
	MaxTokens  int
	MaxDollars float64
	MaxWall    time.Duration
}

// Snapshot is the live spend view the TUI renders.
type Snapshot struct {
	Tokens     int
	Dollars    float64
	Wall       time.Duration
	State      State
	HasCeiling bool
}

// Escalation records a threshold crossing (warn or halt).
type Escalation struct {
	State  State
	Reason string
}

// Accountant tracks spend. Safe for concurrent use.
type Accountant struct {
	mu          sync.Mutex
	tokens      int
	dollars     float64
	start       time.Time
	now         func() time.Time
	ceiling     *Ceiling
	escalations []Escalation
	lastState   State
}

// New returns an always-on accountant with NO ceiling (the default posture).
func New() *Accountant {
	return &Accountant{now: time.Now, start: time.Now(), lastState: StateOK}
}

// FromEnv returns the accountant the environment asks for (or-v9f.18):
// ORION_BUDGET_MAX_TOKENS, ORION_BUDGET_MAX_DOLLARS, and
// ORION_BUDGET_MAX_WALL_MINUTES set the opt-in ceiling axes; with none set the
// accountant is accounting-only and never halts (the default posture). An
// unparseable value is ignored (never a fatal misconfiguration).
func FromEnv() *Accountant {
	var c Ceiling
	if v, err := strconv.Atoi(os.Getenv("ORION_BUDGET_MAX_TOKENS")); err == nil && v > 0 {
		c.MaxTokens = v
	}
	if v, err := strconv.ParseFloat(os.Getenv("ORION_BUDGET_MAX_DOLLARS"), 64); err == nil && v > 0 {
		c.MaxDollars = v
	}
	if v, err := strconv.Atoi(os.Getenv("ORION_BUDGET_MAX_WALL_MINUTES")); err == nil && v > 0 {
		c.MaxWall = time.Duration(v) * time.Minute
	}
	if c == (Ceiling{}) {
		return New()
	}
	return NewWithCeiling(c)
}

// NewWithCeiling returns an accountant with an opt-in hard ceiling.
func NewWithCeiling(c Ceiling) *Accountant {
	a := New()
	a.ceiling = &c
	return a
}

// Record adds spend and evaluates the ceiling state, escalating on transitions.
func (a *Accountant) Record(tokens int, dollars float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokens += tokens
	a.dollars += dollars
	a.eval()
}

// Snapshot returns the live spend view (re-evaluates wall-clock state too).
func (a *Accountant) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.eval()
	return Snapshot{
		Tokens:     a.tokens,
		Dollars:    a.dollars,
		Wall:       a.now().Sub(a.start),
		State:      st,
		HasCeiling: a.ceiling != nil,
	}
}

// Halted reports whether a configured ceiling has been reached.
func (a *Accountant) Halted() bool {
	return a.Snapshot().State == StateHalt
}

// Escalations returns the threshold crossings recorded so far.
func (a *Accountant) Escalations() []Escalation {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Escalation, len(a.escalations))
	copy(out, a.escalations)
	return out
}

// eval computes the current state and appends an escalation when the state
// worsens. Caller holds the lock.
func (a *Accountant) eval() State {
	if a.ceiling == nil {
		return StateOK // no ceiling → accounting only, never halts
	}
	frac := a.fraction()
	st := StateOK
	switch {
	case frac >= 1.0:
		st = StateHalt
	case frac >= WarnFraction:
		st = StateWarn
	}
	if worsened(a.lastState, st) {
		a.escalations = append(a.escalations, Escalation{
			State:  st,
			Reason: fmt.Sprintf("budget %s at %.0f%% of ceiling (tokens=%d dollars=%.2f)", st, frac*100, a.tokens, a.dollars),
		})
	}
	a.lastState = st
	return st
}

// fraction is the max utilization across the ceiling's set axes. Caller holds lock.
func (a *Accountant) fraction() float64 {
	c := a.ceiling
	frac := 0.0
	if c.MaxTokens > 0 {
		frac = max(frac, float64(a.tokens)/float64(c.MaxTokens))
	}
	if c.MaxDollars > 0 {
		frac = max(frac, a.dollars/c.MaxDollars)
	}
	if c.MaxWall > 0 {
		frac = max(frac, float64(a.now().Sub(a.start))/float64(c.MaxWall))
	}
	return frac
}

func worsened(prev, cur State) bool {
	return rank(cur) > rank(prev)
}

func rank(s State) int {
	switch s {
	case StateWarn:
		return 1
	case StateHalt:
		return 2
	default:
		return 0
	}
}
