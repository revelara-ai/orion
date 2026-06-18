// Package reliabilitytier maps a project's reliability tier to the rigor the
// proof harness demands (or-uzr / PRD reliability-tier). V2.0 calibrates the
// behavioral mutation-score threshold by tier: a throwaway tool is not
// over-engineered; a critical path is held to a high bar.
//
// Manifesto: reliability calibrated to the project, not maximized blindly.
package reliabilitytier

// Tier is a project's reliability tier.
type Tier string

const (
	Throwaway Tier = "throwaway"
	Standard  Tier = "standard"
	Critical  Tier = "critical"
)

// MutationThreshold is the minimum mutation score (killed/total) the behavioral
// proof must reach at a tier. throwaway < standard < critical.
func MutationThreshold(t Tier) float64 {
	switch t {
	case Throwaway:
		return 0.0
	case Critical:
		return 0.9
	default: // Standard (and unknown) get the middle bar
		return 0.6
	}
}

// Parse normalizes a tier string, defaulting to Standard.
func Parse(s string) Tier {
	switch Tier(s) {
	case Throwaway:
		return Throwaway
	case Critical:
		return Critical
	default:
		return Standard
	}
}
