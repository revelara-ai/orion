package verify

import (
	"context"
	"fmt"
	"time"

	"github.com/revelara-ai/orion/internal/harness"
	"github.com/revelara-ai/orion/internal/stats"
)

// LoopConfig governs the trial loop budget per SPEC §12.6.
type LoopConfig struct {
	// MinTrials is the floor before any decisive-baseline rejection
	// fires. Default 8 paired trials.
	MinTrials int

	// MaxTrials is the cap. Defaults per Thoroughness:
	// fast=12, standard=24, thorough=48.
	MaxTrials int

	// OverallConfidence is the per-axis CI confidence level pre-Bonferroni.
	// Default 0.95.
	OverallConfidence float64

	// DecisiveWidth is the maximum acceptable CI width for an
	// "accepted" verdict (axes must be both decisively above zero AND
	// have CI widths below this). Defaults to math.Inf(1) effectively
	// disabling the width gate when set to 0; v1 default is the
	// disabled mode.
	DecisiveWidth float64

	// BootstrapReplicates per BCa call. Default 2000.
	BootstrapReplicates int

	// BCaSeed seeds the bootstrap RNG. Recorded for reproducibility.
	BCaSeed int64
}

// DefaultLoopConfig returns a LoopConfig matching SPEC §12.6 standard
// thoroughness defaults.
func DefaultLoopConfig() LoopConfig {
	return LoopConfig{
		MinTrials:           8,
		MaxTrials:           24,
		OverallConfidence:   0.95,
		DecisiveWidth:       0, // disabled
		BootstrapReplicates: 2000,
		BCaSeed:             1,
	}
}

// LoopConfigForThoroughness returns the v1 default config for a
// harness Thoroughness tier.
func LoopConfigForThoroughness(t harness.Thoroughness) LoopConfig {
	cfg := DefaultLoopConfig()
	switch t {
	case harness.ThoroughnessFast:
		cfg.MaxTrials = 12
	case harness.ThoroughnessThorough:
		cfg.MaxTrials = 48
	}
	return cfg
}

// PairResult is one paired trial: baseline metrics + patched metrics.
type PairResult struct {
	Baseline *harness.Metrics
	Patched  *harness.Metrics
}

// Loop runs interleaved baseline-vs-patched trials, computes BCa CIs
// per axis after each pair, and terminates when:
//
//  1. After at least cfg.MinTrials pairs: any axis decisively favors
//     baseline → DecisionRejectedRegression.
//  2. After at least cfg.MinTrials pairs: every axis decisively favors
//     patched (and CI widths are below DecisiveWidth, when set) →
//     DecisionAccepted.
//  3. After at least cfg.MinTrials pairs: NO axis bound favors
//     patched → DecisionRejectedNoDominance.
//  4. cfg.MaxTrials reached without decision → DecisionRejectedLowConfidence.
//
// baselineRunner and patchedRunner share the same harness; the
// PatchedDelta on patchedRunner is what produces the observed
// difference. In v1 production, both are K8s runners differing only
// by which workspace they target (clean vs patched); v1 in-process
// runners differ by their PatchedDelta config.
func Loop(
	ctx context.Context,
	h *harness.Harness,
	baselineRunner harness.Runner,
	patchedRunner harness.Runner,
	cfg LoopConfig,
) (*Verdict, error) {
	if h == nil {
		return nil, fmt.Errorf("%w: nil harness", ErrInvalidInputs)
	}
	if baselineRunner == nil || patchedRunner == nil {
		return nil, fmt.Errorf("%w: runners required", ErrInvalidInputs)
	}
	if cfg.MinTrials <= 0 {
		cfg.MinTrials = 8
	}
	if cfg.MaxTrials < cfg.MinTrials {
		cfg.MaxTrials = cfg.MinTrials
	}
	if cfg.OverallConfidence <= 0 || cfg.OverallConfidence >= 1 {
		cfg.OverallConfidence = 0.95
	}
	if cfg.BootstrapReplicates <= 0 {
		cfg.BootstrapReplicates = 2000
	}

	axes := AllAxes()
	overallAlpha := 1.0 - cfg.OverallConfidence

	// Per-axis delta samples: positive delta = patched is better than
	// baseline. Conventions per axis (lower-better vs higher-better)
	// are encoded in deltaForAxis.
	deltaSamples := map[Axis][]float64{}
	for _, a := range axes {
		deltaSamples[a] = []float64{}
	}
	baselineSamples := map[Axis][]float64{}
	patchedSamples := map[Axis][]float64{}
	for _, a := range axes {
		baselineSamples[a] = []float64{}
		patchedSamples[a] = []float64{}
	}

	var verdict Verdict
	verdict.HarnessSeed = h.Seed
	verdict.MaxTrials = cfg.MaxTrials

	for pair := 1; pair <= cfg.MaxTrials; pair++ {
		bm, err := baselineRunner.Run(ctx, h, int64(pair*2))
		if err != nil {
			return nil, fmt.Errorf("baseline trial %d: %w", pair, err)
		}
		pm, err := patchedRunner.Run(ctx, h, int64(pair*2+1))
		if err != nil {
			return nil, fmt.Errorf("patched trial %d: %w", pair, err)
		}
		for _, a := range axes {
			b := axisValue(bm, a)
			p := axisValue(pm, a)
			baselineSamples[a] = append(baselineSamples[a], b)
			patchedSamples[a] = append(patchedSamples[a], p)
			deltaSamples[a] = append(deltaSamples[a], deltaForAxis(a, b, p))
		}
		verdict.PairedTrialsConsumed = pair
		if pair < cfg.MinTrials {
			continue
		}

		// Pocock-adjusted per-look alpha controls family-wise Type I
		// error across early-termination checks; Bonferroni splits it
		// further across the per-axis CIs.
		perLookAlpha, perr := stats.PocockBoundary(pair, cfg.MaxTrials, overallAlpha)
		if perr != nil {
			return nil, perr
		}
		perAxisLevel, perr := stats.BonferroniAdjust(1.0-perLookAlpha, len(axes))
		if perr != nil {
			return nil, perr
		}
		decision, axisRollups, terminate := evaluate(axes, baselineSamples, patchedSamples, deltaSamples, perAxisLevel, cfg)
		if terminate {
			verdict.Decision = decision
			verdict.Axes = axisRollups
			verdict.CompletedAt = time.Now().UTC()
			return &verdict, nil
		}
	}

	// Reached MaxTrials without decision. Compute final rollups at the
	// last-look adjusted per-axis level so the Verdict's CI bounds
	// reflect the same correction applied during the loop.
	finalLookAlpha, _ := stats.PocockBoundary(cfg.MaxTrials, cfg.MaxTrials, overallAlpha)
	finalAxisLevel, _ := stats.BonferroniAdjust(1.0-finalLookAlpha, len(axes))
	_, rollups, _ := evaluate(axes, baselineSamples, patchedSamples, deltaSamples, finalAxisLevel, cfg)
	verdict.Decision = DecisionRejectedLowConfidence
	verdict.Axes = rollups
	verdict.CompletedAt = time.Now().UTC()
	return &verdict, nil
}

// axisValue extracts the per-axis float value from harness.Metrics.
func axisValue(m *harness.Metrics, a Axis) float64 {
	switch a {
	case AxisLatencyP99:
		return float64(m.LatencyP99Ms)
	case AxisErrorRate:
		return m.ErrorRate
	case AxisCascadeProb:
		return m.CascadeProbability
	case AxisPeakMemoryBytes:
		return float64(m.PeakMemoryBytes)
	}
	return 0
}

// deltaForAxis encodes "positive delta = patched is better".
// All four v1 axes are lower-better (latency, errors, cascade
// probability, memory), so delta = baseline - patched.
func deltaForAxis(_ Axis, baseline, patched float64) float64 {
	return baseline - patched
}

// evaluate runs the per-pair decision: compute BCa CI for each axis's
// delta samples, then apply the SPEC §12.6 decision matrix.
func evaluate(
	axes []Axis,
	baselineSamples, patchedSamples, deltaSamples map[Axis][]float64,
	perAxisLevel float64,
	cfg LoopConfig,
) (Decision, []AxisMetrics, bool) {
	rollups := make([]AxisMetrics, 0, len(axes))
	allDecisive := true
	anyRegression := false
	anyFavorPatched := false

	for axisIdx, a := range axes {
		ci, err := stats.BCa(deltaSamples[a], stats.BCaOptions{
			Level:      perAxisLevel,
			Replicates: cfg.BootstrapReplicates,
			Seed:       cfg.BCaSeed + int64(axisIdx),
		})
		if err != nil {
			// Treat unscorable axis as indeterminate.
			rollups = append(rollups, AxisMetrics{
				Axis:         a,
				BaselineMean: stats.Mean(baselineSamples[a]),
				PatchedMean:  stats.Mean(patchedSamples[a]),
				TrialCount:   len(deltaSamples[a]),
				Decision:     stats.AxisIndeterminate,
			})
			allDecisive = false
			continue
		}
		decision := axisDecision(ci, cfg.DecisiveWidth)
		rollups = append(rollups, AxisMetrics{
			Axis:         a,
			BaselineMean: stats.Mean(baselineSamples[a]),
			PatchedMean:  stats.Mean(patchedSamples[a]),
			DeltaCI:      ci,
			TrialCount:   len(deltaSamples[a]),
			Decision:     decision,
		})
		if decision != stats.AxisDecisive {
			allDecisive = false
		}
		if decision == stats.AxisRegression {
			anyRegression = true
		}
		if ci.FavorsPatched() {
			anyFavorPatched = true
		}
	}

	if anyRegression {
		return DecisionRejectedRegression, rollups, true
	}
	if allDecisive {
		return DecisionAccepted, rollups, true
	}
	if !anyFavorPatched {
		return DecisionRejectedNoDominance, rollups, true
	}
	return DecisionRejectedLowConfidence, rollups, false
}

// axisDecision applies the per-axis decision matrix (CI vs zero, plus
// optional width gate).
func axisDecision(ci stats.ConfidenceInterval, decisiveWidth float64) stats.AxisDecision {
	if ci.FavorsBaseline() {
		return stats.AxisRegression
	}
	if ci.FavorsPatched() {
		if decisiveWidth > 0 && ci.Width() > decisiveWidth {
			return stats.AxisIndeterminate
		}
		return stats.AxisDecisive
	}
	return stats.AxisIndeterminate
}
