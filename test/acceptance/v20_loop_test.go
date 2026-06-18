package acceptance

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// canonicalIntent is the V2.0 acceptance scenario: deliberately ambiguous
// (format? timezone? port? route?) so the completeness gate must grill rather
// than silently guess.
const canonicalIntent = "Build an HTTP service that returns the current time."

// canonicalAnswers resolve the deliberate ambiguities. These define the
// non-interactive answer contract the loop must accept (`orion answer`).
var canonicalAnswers = []struct{ Key, Value string }{
	{"response_format", "json"},
	{"timezone", "UTC"},
	{"port", "8080"},
	{"route", "/time"},
}

// noPackagesMarkers signal that a `go test ./pkg/...` matched nothing or failed
// to build the target — which must read as FAIL (RED), never as a pass.
var noPackagesMarkers = []string{
	"no packages to test",
	"matched no packages",
	"no Go files",
	"cannot find package",
	"cannot find module",
	"no required module provides package",
	"is not in std",
	"does not contain main module",
	"directory prefix",
}

// moduleRoot walks up from the test's working directory to the dir holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found walking up from working directory")
		}
		dir = parent
	}
}

// setupDataDir creates an isolated ORION_DATA_DIR (PRD harness-isolation
// requirement) and returns it with a cleanup func.
func setupDataDir(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "orion-acceptance-*")
	if err != nil {
		t.Fatalf("mktemp ORION_DATA_DIR: %v", err)
	}
	return dir, func() { _ = os.RemoveAll(dir) }
}

// buildOrion builds the single `orion` binary the CLI predicates exercise.
// Returns ok=false (with build output) when cmd/orion does not yet exist — the
// expected state until the loop builds it. CLI predicates then hard-fail rather
// than spuriously passing on a "command not found" exit.
func buildOrion(t *testing.T, root string) (binPath string, ok bool, output string) {
	t.Helper()
	binDir, err := os.MkdirTemp("", "orion-bin-*")
	if err != nil {
		t.Fatalf("mktemp bin dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(binDir) })
	binPath = filepath.Join(binDir, "orion")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/orion")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return binPath, false, string(out)
	}
	if _, statErr := os.Stat(binPath); statErr != nil {
		return binPath, false, "build reported success but binary is missing"
	}
	return binPath, true, string(out)
}

// scriptEnv builds the environment for a predicate: isolated ORION_DATA_DIR and
// the freshly-built orion binary first on PATH.
func scriptEnv(dataDir, binDir string) []string {
	env := os.Environ()
	out := env[:0:0]
	for _, e := range env {
		if strings.HasPrefix(e, "ORION_DATA_DIR=") || strings.HasPrefix(e, "PATH=") {
			continue
		}
		out = append(out, e)
	}
	out = append(out, "ORION_DATA_DIR="+dataDir)
	out = append(out, "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return out
}

// runScript runs one predicate under bash with pipefail, from the module root.
func runScript(script, dir string, env []string) (exitCode int, output string) {
	cmd := exec.Command("bash", "-c", "set -o pipefail; "+script)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), string(out)
	}
	return -1, string(out) + "\n" + err.Error()
}

// driveLoop runs the canonical V2.0 scenario end-to-end against the CLI so that
// the CLI predicates (spec/plan/task/proof/deliver) have real state to assert
// against. It is best-effort: failures here are recorded but do not abort the
// run, because every predicate is independently graded below. This documents the
// non-interactive CLI contract the loop must satisfy.
func driveLoop(t *testing.T, dir string, env []string) {
	t.Helper()
	steps := []string{
		"orion init",
		fmt.Sprintf(`printf %%s %q | orion submit --non-interactive`, canonicalIntent),
	}
	for _, a := range canonicalAnswers {
		steps = append(steps, fmt.Sprintf("orion answer --key %s --value %s", a.Key, a.Value))
	}
	steps = append(steps, "orion spec approve", "orion run")
	for _, s := range steps {
		if code, out := runScript(s, dir, env); code != 0 {
			t.Logf("driveLoop step failed (expected while RED): %s -> exit %d\n%s", s, code, strings.TrimSpace(out))
		}
	}
}

// TestV20Loop is the Orion V2.0 integration acceptance target (or-9xl). It is
// the definition of "done" for V2.0: every shell-verifiable predicate from the
// PRD must pass. It is RED until the loop fills it in — that is the point.
func TestV20Loop(t *testing.T) {
	if len(predicates) == 0 {
		t.Fatal("no acceptance predicates encoded")
	}

	root := moduleRoot(t)
	dataDir, cleanup := setupDataDir(t)
	defer cleanup()

	binPath, binOK, buildOut := buildOrion(t, root)
	binDir := filepath.Dir(binPath)
	env := scriptEnv(dataDir, binDir)

	if binOK {
		driveLoop(t, root, env)
	} else {
		t.Logf("orion binary not built yet (expected while RED): %s", strings.TrimSpace(firstLines(buildOut, 8)))
	}

	type result struct {
		category string
		name     string
		passed   bool
	}
	var results []result

	for _, p := range predicates {
		t.Run(p.Category+"/"+p.Name, func(t *testing.T) {
			start := time.Now()

			if p.Kind == kindCLI && !binOK {
				results = append(results, result{p.Category, p.Name, false})
				t.Fatalf("CLI predicate cannot pass: `orion` binary not built (cmd/orion missing)\nbuild output:\n%s", firstLines(buildOut, 8))
			}

			// For go-test predicates, inject -v at execution so we can require the
			// *named* test to have actually run and passed. `go test -run X` exits 0
			// even when X matches nothing (e.g. `./...` with no such test) — which
			// would be a false green. This makes the gate stricter, never looser.
			execScript := p.Script
			if p.Kind == kindGoTest {
				execScript = strings.ReplaceAll(execScript, "go test ", "go test -v ")
			}
			code, out := runScript(execScript, root, env)
			passed := code == 0
			// A CLI predicate must not read as a pass when the subcommand is
			// unimplemented. `orion` prints a recognizable marker and exits non-zero
			// for unknown/not-implemented commands; some predicates invert the exit
			// code (e.g. the negative `deps verify` case), so guard on the marker
			// regardless of exit status.
			if p.Kind == kindCLI {
				lc := strings.ToLower(out)
				if strings.Contains(lc, "not implemented") || strings.Contains(lc, "unknown command") {
					passed = false
				}
			}
			if passed && p.Kind == kindGoTest {
				lc := strings.ToLower(out)
				for _, m := range noPackagesMarkers {
					if strings.Contains(lc, m) {
						passed = false
						break
					}
				}
				// Require the named test to have run and passed, with no failures.
				if passed && (!strings.Contains(out, "--- PASS:") || strings.Contains(out, "--- FAIL:")) {
					passed = false
				}
			}
			results = append(results, result{p.Category, p.Name, passed})

			if !passed {
				t.Fatalf("predicate FAILED (exit %d, %s)\n  $ %s\n%s",
					code, time.Since(start).Round(time.Millisecond), p.Script, firstLines(out, 12))
			}
		})
	}

	// Rollup: counts by category + overall. Always printed so the RED surface is
	// legible as the loop drives it green.
	byCat := map[string][2]int{} // [passed, total]
	total, passed := 0, 0
	for _, r := range results {
		c := byCat[r.category]
		c[1]++
		if r.passed {
			c[0]++
			passed++
		}
		total++
		byCat[r.category] = c
	}
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	var b strings.Builder
	fmt.Fprintf(&b, "\n=== Orion V2.0 acceptance rollup (or-9xl) ===\n")
	fmt.Fprintf(&b, "orion binary built: %v\n", binOK)
	for _, c := range cats {
		v := byCat[c]
		fmt.Fprintf(&b, "  %-16s %d/%d\n", c, v[0], v[1])
	}
	fmt.Fprintf(&b, "  %-16s %d/%d PROVEN\n", "TOTAL", passed, total)
	t.Log(b.String())
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append(lines[:n], "  … (truncated)")
	}
	return strings.Join(lines, "\n")
}
