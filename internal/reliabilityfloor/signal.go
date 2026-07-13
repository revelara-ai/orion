// Package reliabilityfloor sources reliability signals from a corpus and uses them
// twice: as advisory context for the generator and as log-only golangci-lint checks.
// Distinct from reliabilityscan/reliabilitytier (local static tier classification).
package reliabilityfloor

import (
	"context"
	"strings"
)

// Severity ranks a floor signal's weight.
type Severity int

// Severity levels, ascending.
const (
	SevLow Severity = iota
	SevMedium
	SevHigh
	SevCritical
)

// ParseSeverity maps corpus severity strings onto the closed scale (unknown → low).
func ParseSeverity(s string) Severity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return SevCritical
	case "high":
		return SevHigh
	case "medium":
		return SevMedium
	default:
		return SevLow
	}
}

// CheckKind names the deterministic check a signal can drive.
type CheckKind string

// Check kinds.
const (
	CheckNone         CheckKind = "none"
	CheckGolangciLint CheckKind = "golangci-lint"
)

// Check is the deterministic check derived from a signal.
type Check struct {
	Kind    CheckKind
	Linters []string
}

// Signal is one corpus-sourced reliability fact scoped to the change.
type Signal struct {
	ID       string // RC-XXX | R-XXX | incident short_name
	Title    string
	Why      string
	Severity Severity
	Source   string // control | risk | knowledge
	Check    Check
}

// SignalSource fetches raw reliability signals for a project + query. Implementations
// MUST fail open: return (nil, nil) on auth/parse/network failure, never a hard error
// that would abort a change.
type SignalSource interface {
	Fetch(ctx context.Context, projectID, query string) ([]Signal, error)
}
