package stats

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
)

// BCaOptions configures a BCa bootstrap call.
type BCaOptions struct {
	// Level is the confidence level (e.g. 0.95). Must be in (0, 1).
	Level float64

	// Replicates is the number of bootstrap resamples. Default 2000.
	Replicates int

	// Seed deterministically governs the bootstrap. Required for
	// reproducibility (the verifier records the seed in the Verdict).
	Seed int64

	// Statistic is the function applied to a sample to produce the
	// scalar of interest. Default: arithmetic mean.
	Statistic func([]float64) float64
}

// Mean is the default Statistic.
func Mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range xs {
		s += v
	}
	return s / float64(len(xs))
}

// BCa returns the bias-corrected accelerated bootstrap confidence
// interval for sample using opts.Statistic at opts.Level. Implements
// the Efron 1987 algorithm:
//
//  1. observed = Statistic(sample)
//  2. Generate Replicates bootstrap samples (sample with replacement),
//     compute Statistic on each.
//  3. z0 = Φ⁻¹(proportion of bootstrap statistics < observed)
//  4. a = Σ(mean - jack[i])³ / (6 * (Σ(mean - jack[i])²)^(3/2))  via jackknife
//  5. CI = sorted bootstrap[α₁], sorted bootstrap[α₂] where
//     α₁ = Φ(z0 + (z0 + z_α/2) / (1 - a*(z0 + z_α/2)))
//     α₂ = Φ(z0 + (z0 + z_(1-α/2)) / (1 - a*(z0 + z_(1-α/2))))
//
// Edge cases:
//   - If all bootstrap statistics equal observed, z0=0 (degenerate
//     case; CI collapses to observed).
//   - If jackknife variance is zero, a=0 (the BCa reduces to BC,
//     bias-corrected only).
//   - Single-element sample: returns CI = [observed, observed].
func BCa(sample []float64, opts BCaOptions) (ConfidenceInterval, error) {
	if len(sample) == 0 {
		return ConfidenceInterval{}, ErrEmptySample
	}
	if opts.Level <= 0 || opts.Level >= 1 {
		return ConfidenceInterval{}, ErrInvalidLevel
	}
	if opts.Statistic == nil {
		opts.Statistic = Mean
	}
	if opts.Replicates <= 0 {
		opts.Replicates = 2000
	}

	observed := opts.Statistic(sample)
	if len(sample) == 1 {
		return ConfidenceInterval{Lower: observed, Upper: observed, Level: opts.Level}, nil
	}

	// Bootstrap.
	rng := rand.New(rand.NewSource(opts.Seed)) //#nosec G404 -- determinism > crypto strength
	boot := make([]float64, opts.Replicates)
	resample := make([]float64, len(sample))
	for r := 0; r < opts.Replicates; r++ {
		for i := range resample {
			resample[i] = sample[rng.Intn(len(sample))]
		}
		boot[r] = opts.Statistic(resample)
	}
	sort.Float64s(boot)

	// Bias correction z0.
	below := 0
	for _, b := range boot {
		if b < observed {
			below++
		}
	}
	prop := float64(below) / float64(len(boot))
	z0 := normalQuantile(prop)
	if math.IsInf(z0, 0) || math.IsNaN(z0) {
		// Degenerate: all bootstrap stats are above or equal to observed.
		z0 = 0
	}

	// Acceleration a from jackknife.
	jack := make([]float64, len(sample))
	for i := range sample {
		// Leave-one-out
		loo := append([]float64{}, sample[:i]...)
		loo = append(loo, sample[i+1:]...)
		jack[i] = opts.Statistic(loo)
	}
	jackMean := Mean(jack)
	num := 0.0
	den := 0.0
	for _, j := range jack {
		d := jackMean - j
		num += d * d * d
		den += d * d
	}
	var a float64
	if den == 0 {
		a = 0
	} else {
		a = num / (6 * math.Pow(den, 1.5))
	}

	// Adjusted percentiles.
	alpha := 1.0 - opts.Level
	zLow := normalQuantile(alpha / 2.0)
	zHigh := normalQuantile(1.0 - alpha/2.0)
	a1 := normalCDF(z0 + (z0+zLow)/(1.0-a*(z0+zLow)))
	a2 := normalCDF(z0 + (z0+zHigh)/(1.0-a*(z0+zHigh)))
	if math.IsNaN(a1) || math.IsNaN(a2) {
		return ConfidenceInterval{}, fmt.Errorf("stats: BCa adjusted percentiles NaN; sample may be degenerate")
	}
	a1 = clamp01(a1)
	a2 = clamp01(a2)
	if a1 > a2 {
		a1, a2 = a2, a1
	}

	lower := percentile(boot, a1)
	upper := percentile(boot, a2)
	return ConfidenceInterval{Lower: lower, Upper: upper, Level: opts.Level}, nil
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// percentile returns the q-th percentile of a sorted slice using
// nearest-rank.
func percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Round(q*float64(len(sorted)-1) + 0.0))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// normalCDF is Φ, the standard normal cumulative distribution.
func normalCDF(z float64) float64 {
	return 0.5 * (1.0 + math.Erf(z/math.Sqrt(2.0)))
}

// normalQuantile is Φ⁻¹, the standard normal quantile (inverse CDF).
// Uses the Beasley-Springer-Moro approximation; accurate to ~6 digits
// in the tail, plenty for BCa adjustment.
func normalQuantile(p float64) float64 {
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}
	// Beasley-Springer-Moro (1991) algorithm.
	a := []float64{
		-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02,
		1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00,
	}
	b := []float64{
		-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02,
		6.680131188771972e+01, -1.328068155288572e+01,
	}
	c := []float64{
		-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00,
		-2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00,
	}
	d := []float64{
		7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00,
		3.754408661907416e+00,
	}
	const pLow = 0.02425
	const pHigh = 1.0 - pLow

	switch {
	case p < pLow:
		q := math.Sqrt(-2.0 * math.Log(p))
		return (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1.0)
	case p <= pHigh:
		q := p - 0.5
		r := q * q
		return (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q /
			(((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1.0)
	default:
		q := math.Sqrt(-2.0 * math.Log(1.0-p))
		return -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1.0)
	}
}
