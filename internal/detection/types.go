// Package detection wraps the rvl-cli scanner subprocess and produces
// orion-side Findings. The wrapper is intentionally thin: rvl-cli already
// owns matchers, fingerprinting, exclude rules, and language detection;
// orion's job is to invoke it deterministically and adapt its output into
// a stable shape for downstream consumers (synthesis, reporting, audit).
//
// Subprocess rationale: rvl-cli's scanner lives under its `internal/`
// packages, so direct Go-module import is forbidden. One-shot
// `rvl scan --local --format=json` is the supported integration per
// SPEC §3.3 and the §20 #7 subprocess-scope clarification.
package detection

import "errors"

// Finding is the orion-side representation of a single rvl-cli scan
// finding. Field set is the subset of rvl's JSON output orion's downstream
// consumers actually use; new fields are added here, not by exposing rvl's
// raw shape, so that rvl-cli refactors don't break orion contracts.
type Finding struct {
	Slug         string   `json:"slug"`
	Title        string   `json:"title"`
	Category     string   `json:"category"`
	Confidence   string   `json:"confidence"`
	Impact       string   `json:"impact"`
	Likelihood   string   `json:"likelihood"`
	File         string   `json:"file"`
	Line         int      `json:"line"`
	Fingerprint  string   `json:"fingerprint"`
	ControlCodes []string `json:"control_codes,omitempty"`
	Narrative    string   `json:"narrative,omitempty"`
}

// ScanStats summarizes one detection invocation.
type ScanStats struct {
	FindingsTotal int    `json:"findings_total"`
	RvlBinary     string `json:"rvl_binary"`
}

// ScanOptions configures a Scanner.Run invocation.
type ScanOptions struct {
	RepoPath string // absolute path to the cloned target repo
	Service  string // service name passed to rvl --service
}

// ScannerConfig is the constructor input for NewScanner.
type ScannerConfig struct {
	// Runner is the subprocess executor. Tests inject a fake; production
	// callers leave it nil and the constructor falls back to execRunner.
	Runner Runner
	// RvlBinary is the rvl executable name or path. Defaults to "rvl"
	// when empty (resolved via $PATH at exec time).
	RvlBinary string
}

// Sentinel error classes for callers to errors.Is against.
var (
	// ErrInvalidOptions: caller passed a bad ScanOptions (empty repo or
	// service). Surfaced before any subprocess invocation.
	ErrInvalidOptions = errors.New("detection: invalid scan options")

	// ErrSubprocessFailure: rvl exited non-zero or could not be launched.
	// Wrapped error carries the underlying os/exec error.
	ErrSubprocessFailure = errors.New("detection: rvl subprocess failed")

	// ErrParseFailure: rvl produced output that could not be parsed as the
	// expected JSON envelope.
	ErrParseFailure = errors.New("detection: rvl output is not valid JSON")
)
