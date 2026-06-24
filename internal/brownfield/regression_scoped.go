package brownfield

import (
	"context"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/proof/safeenv"
)

// BaselineScoped runs the Go toolchain's tests restricted to the given package patterns
// (e.g. ["./a"]). Empty patterns ≡ the full suite (./...). Same untrusted-code isolation
// (safeenv) as Baseline.
func BaselineScoped(ctx context.Context, repoDir string, patterns []string) (TestResult, error) {
	tc, ok := DetectToolchain(repoDir)
	if !ok {
		return TestResult{Skipped: "no known toolchain (looked for go.mod)"}, nil
	}
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	args := append([]string{"test"}, patterns...)
	cmd := exec.CommandContext(ctx, tc.TestCmd[0], args...) // tc.TestCmd[0] == "go"
	cmd.Dir = repoDir
	cmd.Env = safeenv.Build() // untrusted repo code never sees host secrets
	out, err := cmd.CombinedOutput()
	return TestResult{
		Detected:  true,
		Toolchain: tc.Name,
		Command:   "go " + strings.Join(args, " "),
		Passed:    err == nil,
		Output:    clip(string(out), 8000),
	}, nil
}

// RegressionGateScoped restricts the regression gate to the changed packages + their
// import-graph blast radius (or-3p5.5), so a change to a small corner of a large repo is
// not gated on the whole suite. It applies the change first (so the scope is the ACTUAL
// touched packages), then measures the before-baseline of that scope by stashing the
// change to recover the clean HEAD state, restores it, and measures after.
//
// Sound for regressions within the import graph; regressions reached only via
// runtime/reflection or shared test fixtures OUTSIDE the scope are not covered — keep the
// full ./... gate (RegressionGate) as the default. A change touching no .go packages
// falls back to the full suite.
func RegressionGateScoped(ctx context.Context, repoDir string, m RepoMap, apply func() error) (RegressionResult, error) {
	if _, ok := DetectToolchain(repoDir); !ok {
		return RegressionResult{Reason: "no test toolchain — cannot establish a regression baseline"}, nil
	}
	if apply != nil {
		if err := apply(); err != nil {
			return RegressionResult{Reason: "applying the change failed: " + err.Error()}, nil
		}
	}

	pats := scopePatterns(m, changedGoDirs(ctx, repoDir)) // nil → full ./... (safe fallback)

	// Before-of-scope: stash the applied change → clean HEAD → measure → restore.
	stashed, err := gitStashPush(ctx, repoDir)
	if err != nil {
		return RegressionResult{}, err
	}
	before, berr := BaselineScoped(ctx, repoDir, pats)
	if stashed {
		if perr := gitStashPop(ctx, repoDir); perr != nil {
			return RegressionResult{}, perr
		}
	}
	if berr != nil {
		return RegressionResult{}, berr
	}
	res := RegressionResult{Before: before}
	if !before.Passed {
		res.Reason = "baseline is RED before the change (within scope) — fix it green first"
		return res, nil
	}

	after, err := BaselineScoped(ctx, repoDir, pats)
	if err != nil {
		return RegressionResult{}, err
	}
	res.After = after
	if !after.Passed {
		res.Reason = "the change regressed the existing tests (green→red) within scope"
		return res, nil
	}
	res.Held = true
	return res, nil
}

// scopePatterns maps changed package dirs to `go test` patterns covering each changed
// package plus its transitive dependents (blast radius). Empty input → nil (full suite).
func scopePatterns(m RepoMap, changedDirs []string) []string {
	if len(changedDirs) == 0 {
		return nil
	}
	set := map[string]bool{}
	for _, d := range changedDirs {
		set[d] = true
		for _, dep := range m.BlastRadius(d) {
			set[dep] = true
		}
	}
	pats := make([]string, 0, len(set))
	for d := range set {
		pats = append(pats, dirPattern(d))
	}
	sort.Strings(pats)
	return pats
}

func dirPattern(dir string) string {
	dir = filepath.ToSlash(strings.TrimPrefix(dir, "./"))
	if dir == "" || dir == "." {
		return "."
	}
	return "./" + dir
}

// changedGoDirs returns the distinct directories of changed .go files in repoDir (from
// git status), relative to the repo root — the packages the diff touched.
func changedGoDirs(ctx context.Context, repoDir string) []string {
	out, err := gitOutput(ctx, repoDir, "status", "--porcelain")
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	// Porcelain v1 lines are "XY PATH": XY is a fixed 2-col status field (often
	// space-padded, e.g. " M file"), then a space, then the path at index 3. Do NOT
	// trim the leading column — that shifts the offset and corrupts the path.
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 4 {
			continue
		}
		path := line[3:]
		if i := strings.Index(path, " -> "); i >= 0 { // rename: "old -> new"
			path = path[i+4:]
		}
		path = strings.Trim(path, `"`) // git quotes paths with special chars
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		set[filepath.Dir(path)] = true
	}
	dirs := make([]string, 0, len(set))
	for d := range set {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs
}

// --- git plumbing: Orion's own VCS ops on the managed worktree (trusted; no repo code
// runs here, so the host env is fine — untrusted code only runs under BaselineScoped). ---

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	return string(out), err
}

// gitStashPush stashes tracked+untracked changes; reports whether anything was stashed.
func gitStashPush(ctx context.Context, dir string) (bool, error) {
	status, err := gitOutput(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(status) == "" {
		return false, nil // nothing to stash
	}
	if _, err := gitOutput(ctx, dir, "stash", "push", "-u", "-m", "orion-regression-before"); err != nil {
		return false, err
	}
	return true, nil
}

func gitStashPop(ctx context.Context, dir string) error {
	_, err := gitOutput(ctx, dir, "stash", "pop")
	return err
}
