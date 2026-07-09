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
	"bufio"
	"context"
	"fmt"
	"io"
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
	return baselineSkip(ctx, repoDir, nil, nil, "")
}

// baselineSkip runs the full suite while SKIPPING the named tests — the regression-reconciliation
// hook: a change that intentionally supersedes a behavior excludes that behavior's test from the
// do-no-harm requirement, while every OTHER test must still pass. skip entries are go test name
// regexps, OR-joined.
func baselineSkip(ctx context.Context, repoDir string, skip []string, progress Progress, step string) (TestResult, error) {
	tc, ok := DetectToolchain(repoDir)
	if !ok {
		return TestResult{Skipped: "no known toolchain (looked for go.mod)"}, nil
	}
	argv := withGateTimeout(withSkip(tc.TestCmd, skip))
	out, err := runTests(ctx, repoDir, argv, progress, step, 0)
	return TestResult{
		Detected:  true,
		Toolchain: tc.Name,
		Command:   strings.Join(argv, " "),
		Passed:    err == nil,
		Output:    clip(out, 8000),
	}, nil
}

// runTests executes the suite command with the gate's isolated env, streaming
// per-package completion lines to progress as they land while still capturing
// the full combined output for the report. `go test` prints one
// "ok/FAIL/? <pkg> ..." line per package as it finishes, so tee-ing the pipe
// gives live progress without changing how the suite runs (or-m45w: a silent
// 10-minute gate is indistinguishable from a hang). total>0 renders an n/total
// counter (scoped runs know their package count); 0 renders "(n done)".
func runTests(ctx context.Context, repoDir string, argv []string, progress Progress, step string, total int) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = repoDir
	cmd.Env = regressionTestEnv() // untrusted repo code never sees host secrets
	if progress == nil {
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return "", err
	}
	var buf strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
		n := 0
		for sc.Scan() {
			line := sc.Text()
			buf.WriteString(line)
			buf.WriteByte('\n')
			pkg, verdict, ok := packageVerdict(line)
			if !ok {
				continue
			}
			n++
			if total > 0 {
				progress.emit(step, fmt.Sprintf("%s %s (%d/%d)", pkg, verdict, n, total))
			} else {
				progress.emit(step, fmt.Sprintf("%s %s (%d done)", pkg, verdict, n))
			}
		}
		// On a scanner error (pathologically long line) keep draining so the
		// child never blocks on a full pipe; the report is clipped anyway.
		_, _ = io.Copy(io.Discard, pr)
	}()
	err := cmd.Wait()
	pw.Close()
	<-done
	return buf.String(), err
}

// packageVerdict parses one go-test package-completion line: "ok <pkg> 0.3s",
// "FAIL <pkg> [build failed]", "?  <pkg> [no test files]". The bare terminal
// "FAIL" line has no package token and is not a completion; "--- FAIL: Test"
// lines are per-test, not per-package.
func packageVerdict(line string) (pkg, verdict string, ok bool) {
	f := strings.Fields(line)
	if len(f) < 2 {
		return "", "", false
	}
	switch f[0] {
	case "ok", "FAIL", "?":
		return f[1], f[0], true
	}
	return "", "", false
}

// regressionTestEnv is the environment for the gate's `go test` runs: the same
// deny-by-default safeenv (untrusted repo code never sees host secrets) plus an
// explicit ORION_RUN_ACCEPTANCE=false. An aspirational acceptance target that is
// RED by design (it references packages not yet built — e.g. test/acceptance
// TestV20Loop) must NOT be treated as a do-no-harm baseline; a red-by-design test
// cannot detect a green→red regression, so gating on it only produces a permanent
// false block. The test still RUNS by default everywhere else — the gate opts out
// explicitly here, keeping the skip a visible exception rather than the default.
func regressionTestEnv() []string {
	return append(safeenv.Build(), "ORION_RUN_ACCEPTANCE=false")
}

// gateTestTimeout raises go test's per-binary timeout for gate runs. The
// default 10m converts a slow suite on a loaded machine (a local model pinning
// the same cores, a concurrent gate) into a FALSE-RED baseline — an unsound
// verdict in the alarm direction (live incident 2026-07-08: or-4gib). A longer
// timeout never weakens the proof; a test that needs 15 minutes is not less
// proven at a 20-minute ceiling.
const gateTestTimeout = "-timeout=20m"

// withGateTimeout inserts the gate timeout right after the test subcommand.
func withGateTimeout(argv []string) []string {
	if len(argv) < 2 {
		return argv
	}
	out := append([]string{}, argv[:2]...)
	out = append(out, gateTestTimeout)
	return append(out, argv[2:]...)
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
