package brownfield

import "context"

// RegressionResult is the green→green outcome of applying a change to a repo.
type RegressionResult struct {
	Before TestResult // the baseline before the change
	After  TestResult // the suite after the change
	Held   bool       // the change PRESERVED existing behavior (green before AND green after)
	Reason string     // why not held (no green baseline / a test regressed / no toolchain)
}

// RegressionGate is the brownfield "preserve existing behavior" guarantee: it captures
// the green-before baseline, applies the change, then re-runs the suite and requires it
// to STILL be green. A change can only be accepted if the existing tests survive it.
//
//   - If there is no toolchain / no green baseline before, the gate cannot vouch for
//     preservation (Held=false with the reason) — you can't regression-prove against a
//     red or absent baseline.
//   - apply mutates the repo (e.g. the diff generator writes the change). A nil apply
//     just re-measures (before==after).
//
// The suite runs under safeenv (untrusted repo code); full fs/network isolation is the
// sandbox (or-5ym).
// skip names tests whose old assertions a change INTENTIONALLY supersedes — excluded from the
// do-no-harm requirement (a declared, oracle-proven behavior change), while every other test must
// still survive. nil/empty skip = the strict gate.
func RegressionGate(ctx context.Context, repoDir string, skip []string, apply func() error) (RegressionResult, error) {
	before, err := baselineSkip(ctx, repoDir, skip)
	if err != nil {
		return RegressionResult{}, err
	}
	res := RegressionResult{Before: before}
	if !before.Detected {
		res.Reason = "no test toolchain — cannot establish a regression baseline"
		return res, nil
	}
	if !before.Passed {
		res.Reason = "baseline is RED before the change — fix it green before a regression guarantee is possible"
		return res, nil
	}

	if apply != nil {
		if err := apply(); err != nil {
			res.Reason = "applying the change failed: " + err.Error()
			return res, nil
		}
	}

	after, err := baselineSkip(ctx, repoDir, skip)
	if err != nil {
		return RegressionResult{}, err
	}
	res.After = after
	if !after.Passed {
		res.Reason = "the change regressed the existing tests (green→red)"
		return res, nil
	}
	res.Held = true
	return res, nil
}
