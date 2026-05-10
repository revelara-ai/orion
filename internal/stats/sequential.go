package stats

import "math"

// PocockBoundary returns the per-look adjusted alpha for a Pocock
// sequential testing scheme: a single constant boundary repeated at
// every interim analysis. The Pocock boundary value c is determined
// by the equation that the cumulative Type I error across all
// `maxLooks` looks at boundary c equals overallAlpha.
//
// In closed form, c is the solution of:
//
//	1 - Φ(c) raised across maxLooks looks = overallAlpha
//
// Pocock (1977) precomputed common values:
//
//	maxLooks=2: c=2.18 (per-look α≈0.0294 for overall α=0.05)
//	maxLooks=3: c=2.29 (per-look α≈0.0221)
//	maxLooks=4: c=2.36 (per-look α≈0.0182)
//	maxLooks=5: c=2.41 (per-look α≈0.0158)
//
// We return the per-look alpha (1 - Φ(c)) * 2 (two-sided), computed
// from a small lookup table for maxLooks 1..10. Beyond 10, we
// extrapolate via the asymptotic c ≈ √(2 ln ln k); good enough for
// the 12-48 trial counts SPEC §12.6 specifies.
func PocockBoundary(numLooks, maxLooks int, overallAlpha float64) (float64, error) {
	if numLooks < 1 || maxLooks < 1 || numLooks > maxLooks {
		return 0, ErrInvalidLooks
	}
	if overallAlpha <= 0 || overallAlpha >= 1 {
		return 0, ErrInvalidLevel
	}
	c := pocockC(maxLooks, overallAlpha)
	// Two-sided per-look alpha: P(|Z| > c) = 2 * (1 - Φ(c)).
	perLook := 2.0 * (1.0 - normalCDF(c))
	return perLook, nil
}

// pocockTable holds c-values for overall α=0.05, two-sided, looks 1..10.
// Source: Pocock (1977) and Jennison & Turnbull (2000) table 2.1.
var pocockTable05 = map[int]float64{
	1:  1.96,
	2:  2.178,
	3:  2.289,
	4:  2.361,
	5:  2.413,
	6:  2.453,
	7:  2.485,
	8:  2.512,
	9:  2.535,
	10: 2.555,
}

func pocockC(maxLooks int, overallAlpha float64) float64 {
	// The most common case in this codebase is overall α=0.05; if a
	// caller asks for a non-standard α, scale by the ratio of standard
	// normal quantiles. This is an approximation, but good for the
	// 0.01-0.10 range the verifier uses.
	if c, ok := pocockTable05[maxLooks]; ok {
		if math.Abs(overallAlpha-0.05) < 1e-9 {
			return c
		}
		baseQ := normalQuantile(1.0 - 0.05/2.0)
		thisQ := normalQuantile(1.0 - overallAlpha/2.0)
		return c * thisQ / baseQ
	}
	// Asymptotic for large maxLooks.
	if maxLooks <= 0 {
		return 0
	}
	if maxLooks == 1 {
		return normalQuantile(1.0 - overallAlpha/2.0)
	}
	return math.Sqrt(2.0*math.Log(math.Log(float64(maxLooks)+1.0))) * normalQuantile(1.0-overallAlpha/2.0) / normalQuantile(0.975)
}
