// Package verify implements Orion's adaptive statistical verifier per
// SPEC §12.6: take a CandidatePatch and a Harness, run interleaved
// baseline-vs-patched trials, compute per-axis BCa confidence
// intervals after each pair, terminate as soon as a Pocock boundary
// is crossed (decisive improvement, decisive regression, or
// max-trials exhausted), emit a Verdict.
//
// v1 ships:
//   - Standard Efron BCa CIs from internal/stats.
//   - Pocock sequential boundaries (from internal/stats).
//   - Per-axis Bonferroni adjustment (from internal/stats).
//   - Patch applicator that uses `git apply` against a sandbox
//     workspace + `go build` as build verification.
//   - Decision matrix per SPEC §12.6 step 5.
//
// The verifier consumes harness.Runner abstractly. In v1 production it
// will receive a K8s-backed Runner once orion-sfp's deferred
// materializer ships; in tests it receives harness.InProcessRunner
// (deterministic synthetic Metrics).
package verify

import (
	"errors"
	"time"

	"github.com/revelara-ai/orion/internal/stats"
)

// Sentinel errors.
var (
	// ErrInvalidInputs: required field missing or unsafe.
	ErrInvalidInputs = errors.New("verify: invalid inputs")

	// ErrPatchApply: applicator could not apply the patch.
	ErrPatchApply = errors.New("verify: patch apply failed")

	// ErrBuildFailed: post-apply build failed.
	ErrBuildFailed = errors.New("verify: post-apply build failed")
)

// Decision is the top-level verdict per SPEC §12.6.
type Decision string

// Decisions.
const (
	DecisionAccepted              Decision = "accepted"
	DecisionRejectedNoDominance   Decision = "rejected_no_dominance"
	DecisionRejectedRegression    Decision = "rejected_regression"
	DecisionRejectedLowConfidence Decision = "rejected_low_confidence"
)

// Axis is the canonical name of one measured axis. Mirrors
// harness.Metrics field names (lowercased + underscored).
type Axis string

// Axes corresponding to harness.Metrics.
const (
	AxisLatencyP99      Axis = "latency_p99_ms"
	AxisErrorRate       Axis = "error_rate"
	AxisCascadeProb     Axis = "cascade_probability"
	AxisPeakMemoryBytes Axis = "peak_memory_bytes"
)

// AllAxes returns the set the verifier considers per trial.
func AllAxes() []Axis {
	return []Axis{AxisLatencyP99, AxisErrorRate, AxisCascadeProb, AxisPeakMemoryBytes}
}

// AxisMetrics is the per-axis result captured after the loop terminates.
type AxisMetrics struct {
	Axis         Axis                     `json:"axis"`
	BaselineMean float64                  `json:"baseline_mean"`
	PatchedMean  float64                  `json:"patched_mean"`
	DeltaCI      stats.ConfidenceInterval `json:"delta_ci"`
	TrialCount   int                      `json:"trial_count"`
	Decision     stats.AxisDecision       `json:"decision"`
}

// Verdict is the verifier's output.
type Verdict struct {
	// Decision is the composite per SPEC §12.6.
	Decision Decision `json:"decision"`

	// Axes captures per-axis CIs and decisions.
	Axes []AxisMetrics `json:"axes"`

	// PairedTrialsConsumed is the number of (baseline, patched) pairs
	// the loop ran before terminating.
	PairedTrialsConsumed int `json:"paired_trials_consumed"`

	// MaxTrials is the cap that applied for this run.
	MaxTrials int `json:"max_trials"`

	// HarnessSeed is the harness's deterministic seed; recorded for
	// reproduction.
	HarnessSeed int64 `json:"harness_seed"`

	// LLMModel + LLMSeed are the patch's provenance fields.
	LLMModel string `json:"llm_model,omitempty"`
	LLMSeed  int64  `json:"llm_seed,omitempty"`

	// CompletedAt is the wall-clock timestamp at termination.
	CompletedAt time.Time `json:"completed_at"`
}
