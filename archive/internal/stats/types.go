// Package stats implements the statistical primitives Orion's verifier
// uses to make patch-acceptance decisions per SPEC §12.6:
//
//   - BCa bootstrap confidence intervals (Efron 1987), robust at small
//     sample sizes and assumption-free.
//   - Sequential-analysis boundaries (Pocock) for early termination of
//     interleaved trial loops with controlled family-wise Type I error.
//   - Bonferroni adjustment for per-axis confidence levels when the
//     verifier evaluates multiple axes simultaneously.
//
// The verifier (internal/verify) composes these to compute the
// "patched dominates baseline" decision after each pair of trials.
package stats

import "errors"

// Sentinel errors.
var (
	// ErrEmptySample: empty input passed to a routine that needs samples.
	ErrEmptySample = errors.New("stats: empty sample")

	// ErrInvalidLevel: confidence level outside (0, 1).
	ErrInvalidLevel = errors.New("stats: invalid confidence level")

	// ErrInvalidLooks: numLooks/maxLooks invalid for sequential boundary.
	ErrInvalidLooks = errors.New("stats: invalid sequential looks")
)

// AxisDecision is the per-axis verdict from a confidence-interval
// comparison.
type AxisDecision string

// Per-axis decisions.
const (
	// AxisDecisive means the CI bound favors the alternative (patched
	// over baseline) decisively at the chosen confidence level.
	AxisDecisive AxisDecision = "decisive"

	// AxisRegression means the CI bound favors the null (baseline over
	// patched) decisively.
	AxisRegression AxisDecision = "regression"

	// AxisIndeterminate means the CI bounds straddle zero.
	AxisIndeterminate AxisDecision = "indeterminate"
)

// ConfidenceInterval is a (lower, upper) pair at a given confidence
// level. Bounds are in the units of the observed statistic.
type ConfidenceInterval struct {
	Lower float64
	Upper float64
	Level float64
}

// FavorsPatched reports whether the CI is decisively above zero,
// meaning the patched arm beat baseline (when "delta = baseline - patched"
// or similar caller convention is positive-better).
func (c ConfidenceInterval) FavorsPatched() bool {
	return c.Lower > 0
}

// FavorsBaseline reports the inverse: CI decisively below zero.
func (c ConfidenceInterval) FavorsBaseline() bool {
	return c.Upper < 0
}

// Width returns Upper - Lower.
func (c ConfidenceInterval) Width() float64 {
	return c.Upper - c.Lower
}
