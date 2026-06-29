// Package brownfield is the foundation for applying Orion's proof + validation to
// CHANGES in existing repositories (not just from-scratch services). The first
// primitive is the regression baseline: detect a target repo's toolchain and run
// its own tests to capture green-before — the invariant a proven change must
// preserve (green-after). This is the genuinely-new brownfield concept; greenfield
// has no "before".
//
// SECURITY: running a target repo's tests is running UNTRUSTED code. Every exec here
// uses internal/proof/safeenv (deny-by-default env) so the host environment — which
// holds ANTHROPIC_API_KEY since the native-harness pivot — never reaches the code
// under test. Full process isolation (network/fs via bwrap) is or-5ym.
package brownfield

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/proof/safeenv"
)

// Toolchain is how a repo is built + tested.
type Toolchain struct {
	Name    string   // "go" (more via or-3ba)
	TestCmd []string // argv, e.g. ["go","test","./..."]
}

// TestResult is the outcome of running a repo's existing test suite — the
// regression baseline a brownfield change must not break.
type TestResult struct {
	Detected  bool   // a known toolchain was found
	Toolchain string // its name
	Command   string // the command run
	Passed    bool   // the suite passed (the baseline is green)
	Output    string // combined stdout+stderr (capped)
	Skipped   string // why, if not detected
}

// DetectToolchain inspects repoDir for a known build/test toolchain. Go-only for
// now; or-3ba generalizes to npm/cargo/etc. behind this same interface.
func DetectToolchain(repoDir string) (Toolchain, bool) {
	if exists(filepath.Join(repoDir, "go.mod")) {
		return Toolchain{Name: "go", TestCmd: []string{"go", "test", "./..."}}, true
	}
	return Toolchain{}, false
}

// Baseline detects repoDir's toolchain and runs its existing tests, returning the
// green/red baseline. A missing/unknown toolchain is not an error — it returns
// Detected=false with a reason, so callers can decide (a repo with no tests can't
// offer a regression guarantee). The test exec runs with safeenv (never the host
// env) because the target repo's code is untrusted.
func Baseline(ctx context.Context, repoDir string) (TestResult, error) {
	return baselineSkip(ctx, repoDir, nil)
}

// baselineSkip runs the full suite while SKIPPING the named tests — the regression-reconciliation
// hook: a change that intentionally supersedes a behavior excludes that behavior's test from the
// do-no-harm requirement, while every OTHER test must still pass. skip entries are go test name
// regexps, OR-joined.
func baselineSkip(ctx context.Context, repoDir string, skip []string) (TestResult, error) {
	tc, ok := DetectToolchain(repoDir)
	if !ok {
		return TestResult{Skipped: "no known toolchain (looked for go.mod)"}, nil
	}
	argv := withSkip(tc.TestCmd, skip)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = repoDir
	cmd.Env = safeenv.Build() // untrusted repo code never sees host secrets
	out, err := cmd.CombinedOutput()
	return TestResult{
		Detected:  true,
		Toolchain: tc.Name,
		Command:   strings.Join(argv, " "),
		Passed:    err == nil,
		Output:    clip(string(out), 8000),
	}, nil
}

// withSkip inserts `-skip <re>` right after the test subcommand (argv[1]) when skip is non-empty;
// otherwise returns argv unchanged. The skip set is OR-joined into one regexp.
func withSkip(argv, skip []string) []string {
	if len(skip) == 0 || len(argv) < 2 {
		return argv
	}
	out := append([]string{}, argv[:2]...)
	out = append(out, "-skip", strings.Join(skip, "|"))
	return append(out, argv[2:]...)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "\n… (truncated)"
	}
	return s
}
