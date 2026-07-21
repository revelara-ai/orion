package brownfield

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// baselineDeltaMode (or-cp90) reports whether do-no-harm runs in DELTA mode —
// the default: a change holds if it introduces no NEW failures vs the captured
// baseline, with pre-existing failures excluded BY NAME (never silently). Real
// brownfield repos have red baselines (env-dependent integration tests,
// pre-existing failures on main); demanding absolute green made every change
// unshippable. ORION_REGRESSION_BASELINE=strict restores green-before-required.
func baselineDeltaMode() bool { return os.Getenv("ORION_REGRESSION_BASELINE") != "strict" }

// doNoHarmVerdict fills Held/Reason/PreExisting/NewFailures from the measured
// Before/After runs. PreExisting = baseline failures STILL failing after (the
// excluded set); NewFailures = after-failures absent from the baseline (the
// blocking set). A failed after-run with nothing attributable (panic, timeout
// kill — no parseable failure names) blocks conservatively.
func doNoHarmVerdict(res *RegressionResult, scopeSuffix string) {
	baseline := make(map[string]bool, len(res.Before.Failures))
	for _, f := range res.Before.Failures {
		baseline[f] = true
	}
	afterSet := make(map[string]bool, len(res.After.Failures))
	for _, f := range res.After.Failures {
		afterSet[f] = true
		if !baseline[f] {
			res.NewFailures = append(res.NewFailures, f)
		}
	}
	for _, f := range res.Before.Failures {
		if afterSet[f] {
			res.PreExisting = append(res.PreExisting, f)
		}
	}
	if len(res.NewFailures) > 0 {
		res.Reason = fmt.Sprintf("the change introduced NEW test failures%s: %s", scopeSuffix, strings.Join(res.NewFailures, ", "))
		if n := len(res.PreExisting); n > 0 {
			res.Reason += fmt.Sprintf(" (%d pre-existing failure(s) excluded: %s)", n, clip(strings.Join(res.PreExisting, ", "), 300))
		}
		return
	}
	if !res.After.Passed && len(res.After.Failures) == 0 {
		res.Reason = fmt.Sprintf("the suite failed%s but no failure could be attributed (panic or timeout kill?) — treated as a regression", scopeSuffix)
		return
	}
	res.Held = true
}

// RegressionResult is the green→green outcome of applying a change to a repo.
type RegressionResult struct {
	Before TestResult // the baseline before the change
	After  TestResult // the suite after the change
	Held   bool       // the change introduced no NEW failures (delta, default) / green→green (strict)
	Reason string     // why not held (no green baseline / a test regressed / no toolchain)
	Scope  string     // audit stamp: WHICH scoping argument produced this verdict (full / forced-full / vacuous / test-only / changed+blast)
	// or-cp90 (baseline-delta): the pre-existing baseline failures EXCLUDED from
	// the do-no-harm guarantee (named honestly, never silent), and the NEW
	// failures the change introduced (the blocking set when not Held).
	PreExisting []string
	NewFailures []string
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
func RegressionGate(ctx context.Context, repoDir string, skip []string, apply func() error, progress Progress) (RegressionResult, error) {
	progress.emit("green-before", "running the existing suite (baseline before the change)")
	before, err := baselineSkip(ctx, repoDir, skip, progress, "green-before")
	if err != nil {
		return RegressionResult{}, err
	}
	res := RegressionResult{Before: before, Scope: "full suite (ORION_REGRESSION_SCOPE=full)"}
	if !before.Detected {
		res.Reason = "no test toolchain — cannot establish a regression baseline"
		return res, nil
	}
	if !before.Passed {
		if !baselineDeltaMode() {
			res.Reason = "baseline is RED before the change — fix it green before a regression guarantee is possible"
			return res, nil
		}
		progress.emit("green-before", fmt.Sprintf("baseline is RED (%d pre-existing failure(s)) — delta mode: requiring no NEW failures", len(before.Failures)))
	}

	if apply != nil {
		progress.emit("apply-change", "generating the change in the worktree")
		if err := apply(); err != nil {
			res.Reason = "applying the change failed: " + err.Error()
			return res, nil
		}
	}

	progress.emit("green-after", "re-running the full suite with the change applied")
	after, err := baselineSkip(ctx, repoDir, skip, progress, "green-after")
	if err != nil {
		return RegressionResult{}, err
	}
	res.After = after
	doNoHarmVerdict(&res, "")
	return res, nil
}
