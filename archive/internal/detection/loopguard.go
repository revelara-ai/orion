package detection

import "time"

// LoopGuardThreshold is the ratio above which the §15.4
// self-referential-loop warning fires: orion_filed > 3 ×
// customer_filed.
const LoopGuardThreshold = 3.0

// LoopGuardMinWindow is the minimum age-span the recent runs must
// cover before a warning can fire. Prevents day-one false positives.
const LoopGuardMinWindow = 30 * 24 * time.Hour

// LoopGuardSuppressionRuns is the count of total prior runs for a
// binding below which the warning is always suppressed (the new-
// customer carve-out from SPEC §15.4 final paragraph).
const LoopGuardSuppressionRuns = 3

// RunSummary is the slice of a prior run LoopGuardCheck consumes.
// Mirrors repos.DetectionRun fields without importing the repos
// package, so this file stays at a lower layer in the dep graph.
type RunSummary struct {
	StartedAt              time.Time
	OrionFiledProcessed    int
	CustomerFiledProcessed int
}

// LoopGuardDecision is the per-tick verdict.
type LoopGuardDecision struct {
	Warning bool
	Reason  string
}

// LoopGuardCheck evaluates SPEC §15.4: warn when orion-filed >
// threshold × customer-filed for `LoopGuardSuppressionRuns+1`
// consecutive recent runs (i.e. 3 prior + the current under
// consideration) over a window of at least `LoopGuardMinWindow`.
//
// Inputs:
//
//   - recentRuns: the N-most-recent runs for the binding, ordered
//     newest-first (StartedAt descending).
//   - now: clock for the window check.
//
// Returns Warning=true with a Reason explaining the trigger when
// the threshold is exceeded. False with a Reason explaining the
// suppression otherwise.
func LoopGuardCheck(recentRuns []RunSummary, now time.Time) LoopGuardDecision {
	// Carve-out: fewer than LoopGuardSuppressionRuns prior runs.
	if len(recentRuns) < LoopGuardSuppressionRuns {
		return LoopGuardDecision{
			Warning: false,
			Reason:  "first-3-runs suppression",
		}
	}

	// Window check: oldest of the threshold-set must be older than
	// LoopGuardMinWindow.
	considered := recentRuns
	if len(considered) > LoopGuardSuppressionRuns {
		considered = considered[:LoopGuardSuppressionRuns]
	}
	oldest := considered[len(considered)-1].StartedAt
	if !oldest.IsZero() && now.Sub(oldest) < LoopGuardMinWindow {
		return LoopGuardDecision{
			Warning: false,
			Reason:  "window < 30 days: insufficient history",
		}
	}

	// Threshold: every one of the prior 3 must exceed the ratio.
	for _, r := range considered {
		if r.CustomerFiledProcessed <= 0 {
			// Treat zero-customer-filed as failing the ratio iff
			// orion_filed is non-zero (ratio undefined → infinity).
			if r.OrionFiledProcessed <= 0 {
				return LoopGuardDecision{
					Warning: false,
					Reason:  "one or more runs had zero detection activity",
				}
			}
			continue
		}
		ratio := float64(r.OrionFiledProcessed) / float64(r.CustomerFiledProcessed)
		if ratio <= LoopGuardThreshold {
			return LoopGuardDecision{
				Warning: false,
				Reason:  "one or more runs below the 3x threshold",
			}
		}
	}

	return LoopGuardDecision{
		Warning: true,
		Reason:  "orion-filed > 3x customer-filed for 3 consecutive runs spanning >= 30d",
	}
}
