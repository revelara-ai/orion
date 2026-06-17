package detection

import "sort"

// ProgressiveDisclosureCap encodes SPEC §15.2 phase 5: bound the
// number of newly-found gaps Orion auto-files in one tick so the
// customer's review queue does not balloon faster than they can
// burn it down.
//
// The cap returns up to:
//
//	min(MaxPerRun, TargetDepth - currentEligibleCount)
//
// findings. If the count is non-positive (target already met or
// exceeded), Filter returns nothing for this tick; the remainder
// will be re-considered next tick once backlog has been worked off.
//
// Ordering: findings are sorted by TrustScoreFn(slug) DESC. v1
// defaults every slug to 1.0, so order falls through to a stable
// secondary sort by Fingerprint then File:Line. E9 supplies real
// per-pattern trust scores.
type ProgressiveDisclosureCap struct {
	MaxPerRun     int
	TargetDepth   int
	EligibleCount int
	TrustScoreFn  func(slug string) float64
}

// DefaultMaxPerRun mirrors SPEC §15.2 phase 5's scan.max_auto_filed_per_run.
const DefaultMaxPerRun = 25

// CapLimit returns the per-tick maximum filings allowed given the
// inputs. Exposed for callers (loopdriver, observability) that want
// to log the cap separately from applying it.
func (c ProgressiveDisclosureCap) CapLimit() int {
	maxPerRun := c.MaxPerRun
	if maxPerRun <= 0 {
		maxPerRun = DefaultMaxPerRun
	}
	headroom := c.TargetDepth - c.EligibleCount
	if headroom < 0 {
		headroom = 0
	}
	if c.TargetDepth <= 0 {
		// No target configured → only max_per_run binds.
		return maxPerRun
	}
	if headroom < maxPerRun {
		return headroom
	}
	return maxPerRun
}

// Filter applies the cap to a slice of new-gap findings. Returns a
// new slice (the input is not mutated) containing at most CapLimit()
// elements, sorted by trust score DESC + fingerprint + file:line.
//
// Pre-cap ordering is stable so callers can predict which findings
// land per tick when running against an unchanged fixture (orion-e3a
// + orion-e3f assertions depend on this).
func (c ProgressiveDisclosureCap) Filter(findings []Finding) []Finding {
	if len(findings) == 0 {
		return findings
	}
	trust := c.TrustScoreFn
	if trust == nil {
		trust = func(string) float64 { return 1.0 }
	}

	sorted := make([]Finding, len(findings))
	copy(sorted, findings)
	sort.SliceStable(sorted, func(i, j int) bool {
		ti := trust(sorted[i].Slug)
		tj := trust(sorted[j].Slug)
		if ti != tj {
			return ti > tj // DESC
		}
		if sorted[i].Fingerprint != sorted[j].Fingerprint {
			return sorted[i].Fingerprint < sorted[j].Fingerprint
		}
		if sorted[i].File != sorted[j].File {
			return sorted[i].File < sorted[j].File
		}
		return sorted[i].Line < sorted[j].Line
	})

	limit := c.CapLimit()
	if limit < 0 {
		limit = 0
	}
	if limit >= len(sorted) {
		return sorted
	}
	return sorted[:limit]
}
