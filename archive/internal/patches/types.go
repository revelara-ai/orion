// Package patches turns detected reliability gaps into LLM-synthesized
// CandidatePatches per SPEC §12.5. The synthesizer takes a Gap (the
// gap detector's output) plus an IssueContextBlock (snapshotted Polaris
// controls + knowledge enrichment), prompts an LLM with code excerpts
// + control text + a constrained patch grammar, and returns one or
// more CandidatePatches that the verifier (E1-7) will then exercise
// against a synthesized harness (E1-5).
//
// v1 supports three reliability patterns: timeout, retry, idempotency.
// The grammar restricts the LLM to producing unified diffs that match
// the per-pattern allowed shape; anything else is rejected by the
// parser before it reaches the verifier.
package patches

import (
	"errors"
	"time"
)

// Sentinel errors for callers to errors.Is against.
var (
	// ErrInvalidGap: a Gap is missing required fields or refers to an
	// unsupported reliability pattern.
	ErrInvalidGap = errors.New("patches: invalid gap")

	// ErrSynthesisFailed: LLM call returned an error or empty body that
	// the parser could not interpret. Wrapped err carries the cause.
	ErrSynthesisFailed = errors.New("patches: synthesis failed")

	// ErrInvalidDiff: parser rejected the LLM-produced unified diff
	// (malformed hunk, missing target_path, banned shape, etc.).
	ErrInvalidDiff = errors.New("patches: invalid diff")
)

// Pattern names the reliability shape this gap addresses. These map 1:1
// to the per-pattern grammars in grammar.go and are wire-stable.
type Pattern string

// Reliability patterns supported by the v1 synthesizer.
const (
	PatternTimeout     Pattern = "timeout"
	PatternRetry       Pattern = "retry"
	PatternIdempotency Pattern = "idempotency"
)

// SupportedPatterns returns the v1 set. Used by the synthesizer to
// reject unsupported patterns before LLM cost is incurred.
func SupportedPatterns() []Pattern {
	return []Pattern{PatternTimeout, PatternRetry, PatternIdempotency}
}

// Gap is the input from the detector: a single reliability gap in one
// file at one location, classified into a Pattern.
type Gap struct {
	// ID is a stable identifier for the gap within a run. Used as the
	// CandidatePatch's GapID for traceability.
	ID string

	// Pattern names the reliability shape (timeout, retry, idempotency).
	Pattern Pattern

	// FilePath is the repo-relative path to the file containing the gap.
	FilePath string

	// LineRange is the 1-indexed [start, end] line range the gap covers.
	// May be a single line ([N, N]).
	LineRange [2]int

	// Description is the detector's natural-language summary of the gap.
	// Surfaced in the LLM prompt to disambiguate sibling gaps.
	Description string

	// CodeExcerpt is the file content for the affected range (with
	// surrounding context). Set by the synthesizer caller from the
	// repo workspace; not the detector's responsibility.
	CodeExcerpt string
}

// Validate checks Gap for the fields the synthesizer requires.
func (g Gap) Validate() error {
	if g.ID == "" || g.FilePath == "" || g.Description == "" {
		return ErrInvalidGap
	}
	if g.LineRange[0] <= 0 || g.LineRange[1] < g.LineRange[0] {
		return ErrInvalidGap
	}
	supported := false
	for _, p := range SupportedPatterns() {
		if g.Pattern == p {
			supported = true
			break
		}
	}
	if !supported {
		return ErrInvalidGap
	}
	return nil
}

// CandidatePatch is one LLM-generated patch for one Gap. The verifier
// (E1-7) will apply it to a working copy and exercise the harness
// against the resulting tree.
type CandidatePatch struct {
	// GapID echoes Gap.ID so downstream pipeline stages can correlate.
	GapID string `json:"gap_id"`

	// TargetPath is the repo-relative path the diff modifies.
	TargetPath string `json:"target_path"`

	// TargetRange is the 1-indexed [start, end] line range the patch
	// targets. May be wider than the Gap's LineRange when surrounding
	// lines are needed for the patch to compile.
	TargetRange [2]int `json:"target_range"`

	// UnifiedDiff is the patch body in unified-diff format. It MUST
	// apply cleanly with `git apply` against the workspace HEAD.
	UnifiedDiff string `json:"unified_diff"`

	// Pattern names the reliability shape addressed.
	Pattern Pattern `json:"pattern"`

	// ControlID is the Polaris control_code this patch addresses, taken
	// from the IssueContextBlock's snapshotted controls. Required.
	ControlID string `json:"control_id"`

	// Provenance: model + seed + timestamp for reproducibility (SPEC §14.6).
	LLMModel    string    `json:"llm_model"`
	LLMSeed     int64     `json:"llm_seed"`
	GeneratedAt time.Time `json:"generated_at"`
}
