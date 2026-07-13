package brownfield

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// BaselineScoped runs the Go toolchain's tests restricted to the given package patterns
// (e.g. ["./a"]). Empty patterns ≡ the full suite (./...). Same untrusted-code isolation
// (safeenv) as Baseline.
func BaselineScoped(ctx context.Context, repoDir string, patterns []string) (TestResult, error) {
	return baselineScopedSkip(ctx, repoDir, patterns, nil, nil, "")
}

// baselineScopedSkip runs the scoped suite while SKIPPING the named tests (the supersession hook,
// same as baselineSkip but restricted to the blast-radius patterns).
func baselineScopedSkip(ctx context.Context, repoDir string, patterns, skip []string, progress Progress, step string) (TestResult, error) {
	tc, ok := DetectToolchain(repoDir)
	if !ok {
		return TestResult{Skipped: "no known toolchain (looked for go.mod)"}, nil
	}
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	total := len(patterns)
	if total == 1 && patterns[0] == "./..." {
		total = 0 // full-suite pattern: package count unknown
	}
	argv := withGateTimeout(withSkip(append([]string{tc.TestCmd[0], "test"}, patterns...), skip))
	out, err := runTests(ctx, repoDir, argv, progress, step, total)
	return TestResult{
		Detected:  true,
		Toolchain: tc.Name,
		Command:   strings.Join(argv, " "),
		Passed:    err == nil,
		Output:    clip(out, 8000),
	}, nil
}

// RegressionGateScoped is the DEFAULT regression gate (or-3p5.5): it restricts the suite to
// the changed packages + their import-graph blast radius, so a change to a small corner of a
// large repo is not gated on the whole suite. It applies the change first (so the scope is the
// ACTUAL touched packages), then measures the before-baseline of that scope by stashing the
// change to recover the clean HEAD state, restores it, and measures after.
//
// Post-apply it picks the scope from what the change touched:
//   - a module/dependency change (go.mod/go.sum/go.work) ESCALATES to the full ./... suite —
//     repo-wide runtime impact the import graph can't capture;
//   - a change touching NO Go package (pure config/docs/Makefile) holds vacuously with zero
//     tests run: the do-no-harm surface is empty, so there is nothing to regress;
//   - otherwise it scopes to the changed packages + their blast radius.
//
// Sound for regressions within the import graph; regressions reached only via build-tag/codegen
// coupling OUTSIDE the import graph are not covered — set ORION_REGRESSION_SCOPE=full (which
// routes to RegressionGate) when a change warrants the whole suite.
// skip names tests whose old assertions a change INTENTIONALLY supersedes (see RegressionGate).
func RegressionGateScoped(ctx context.Context, repoDir string, m RepoMap, skip []string, apply func() error, progress Progress) (RegressionResult, error) {
	if _, ok := DetectToolchain(repoDir); !ok {
		return RegressionResult{Reason: "no test toolchain — cannot establish a regression baseline"}, nil
	}
	if apply != nil {
		progress.emit("apply-change", "generating the change in the worktree")
		if err := apply(); err != nil {
			return RegressionResult{Reason: "applying the change failed: " + err.Error()}, nil
		}
	}

	// Scope decision from the ACTUAL post-apply diff. scopeStamp is the audit
	// trail: every verdict records WHICH scoping argument produced it.
	changed := changedPaths(ctx, repoDir)
	var pats []string
	var scopeStamp string
	switch {
	case regressionForcedFull(changed):
		pats = nil // nil → BaselineScoped runs the full ./... suite
		scopeStamp = "full suite (go.mod/go.sum changed — import graph can't scope a dependency change)"
		progress.emit("scope", "go.mod/go.sum changed — escalating to the full suite")
	case testOnlyChange(changed):
		// Fast path: only _test.go files changed → dependents provably
		// unaffected (compiler forbids cross-package _test.go imports), so the
		// blast radius is skipped and only the changed packages' own tests run.
		scope := scopeDirsForChange(changed, m)
		if len(scope) == 0 {
			scopeStamp = "vacuous (no Go package affected — nothing to regress)"
			progress.emit("scope", "no Go package affected — nothing to regress")
			skip := TestResult{Detected: true, Skipped: "no Go package affected — nothing to regress"}
			return RegressionResult{Held: true, Before: skip, After: skip, Scope: scopeStamp}, nil
		}
		for _, d := range scope {
			pats = append(pats, dirPattern(d))
		}
		sort.Strings(pats)
		scopeStamp = fmt.Sprintf("test-only diff — dependents provably unaffected (compiler: _test.go files cannot be imported); %d package(s): %s", len(pats), clip(strings.Join(pats, " "), 160))
		progress.emit("scope", scopeStamp)
	default:
		scope := scopeDirsForChange(changed, m)
		if len(scope) == 0 {
			scopeStamp = "vacuous (no Go package affected — nothing to regress)"
			progress.emit("scope", "no Go package affected — nothing to regress")
			skip := TestResult{Detected: true, Skipped: "no Go package affected — nothing to regress"}
			return RegressionResult{Held: true, Before: skip, After: skip, Scope: scopeStamp}, nil
		}
		pats = scopePatterns(m, scope)
		scopeStamp = fmt.Sprintf("changed packages + blast radius — %d package(s): %s", len(pats), clip(strings.Join(pats, " "), 160))
		progress.emit("scope", fmt.Sprintf("%d package(s): %s", len(pats), clip(strings.Join(pats, " "), 160)))
	}

	// Before-of-scope: stash the applied change → clean HEAD → measure → restore.
	stashed, err := gitStashPush(ctx, repoDir)
	if err != nil {
		return RegressionResult{}, err
	}
	// or-u595: an unchanged HEAD tree's green baseline is memoized — same
	// (tree, scope, skip, go version) key; GREEN only; dirty trees never hit.
	memoKey, memoOK := baselineMemoKey(ctx, repoDir, pats, skip)
	var before TestResult
	var berr error
	if memoOK {
		if e, hit := loadBaselineMemo(ctx, repoDir, memoKey); hit {
			progress.emit("green-before", "cached green baseline reused (same tree/scope/skip/go)")
			before = cachedBaselineResult(e)
		}
	}
	if !before.Passed {
		progress.emit("green-before", "running the scoped baseline (change stashed)")
		before, berr = baselineScopedSkip(ctx, repoDir, pats, skip, progress, "green-before")
		if berr == nil && memoOK {
			saveBaselineMemo(ctx, repoDir, memoKey, before)
		}
	}
	if stashed {
		if perr := gitStashPop(ctx, repoDir); perr != nil {
			return RegressionResult{}, perr
		}
	}
	if berr != nil {
		return RegressionResult{}, berr
	}
	res := RegressionResult{Before: before, Scope: scopeStamp}
	if !before.Passed {
		res.Reason = "baseline is RED before the change (within scope) — fix it green first"
		return res, nil
	}

	progress.emit("green-after", "re-running the scoped suite with the change applied")
	after, err := baselineScopedSkip(ctx, repoDir, pats, skip, progress, "green-after")
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

// changedPaths returns the distinct changed file paths in repoDir (from git status),
// relative to the repo root.
func changedPaths(ctx context.Context, repoDir string) []string {
	out, err := gitOutput(ctx, repoDir, "status", "--porcelain")
	if err != nil {
		return nil
	}
	var paths []string
	// Porcelain v1 lines are "XY PATH": XY is a fixed 2-col status field (often
	// space-padded, e.g. " M file"), then a space, then the path at index 3. Do NOT
	// trim the leading column — that shifts the offset and corrupts the path.
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 4 {
			continue
		}
		path := line[3:]
		if i := strings.Index(path, " -> "); i >= 0 { // rename: "old -> new" — keep BOTH sides:
			// the source package loses a file (its non-test code changes), and a
			// rename like foo.go -> foo_test.go must never classify as test-only.
			paths = append(paths, strings.Trim(path[:i], `"`))
			path = path[i+4:]
		}
		paths = append(paths, strings.Trim(path, `"`)) // git quotes paths with special chars
	}
	return paths
}

// regressionForcedFull reports whether any changed file forces a full-suite regression: a
// module/dependency change (go.mod/go.sum/go.work[.sum]) has repo-wide runtime impact that the
// internal import graph does not capture, so scoping is unsafe.
func regressionForcedFull(paths []string) bool {
	for _, p := range paths {
		switch filepath.Base(p) {
		case "go.mod", "go.sum", "go.work", "go.work.sum":
			return true
		}
	}
	return false
}

// scopeDirsForChange maps changed files to the package dirs to test: every changed .go file's
// dir (a new or modified Go package), plus any EXISTING package dir that owns a changed non-.go
// file (an embedded asset or testdata fixture). Files outside any Go package (root config, docs,
// Makefile) contribute nothing — so a pure tooling change yields an empty scope (nothing to
// regress).
func scopeDirsForChange(paths []string, m RepoMap) []string {
	pkg := make(map[string]bool, len(m.Packages))
	for _, p := range m.Packages {
		pkg[p.Dir] = true
	}
	set := map[string]bool{}
	for _, p := range paths {
		d := filepath.Dir(p)
		if strings.HasSuffix(p, ".go") || pkg[d] {
			set[d] = true
		}
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

// testOnlyChange reports whether EVERY changed path is a Go test file. The
// compiler forbids importing another package's _test.go files, so a diff that
// touches only _test.go files provably cannot alter any dependent package's
// behavior — the do-no-harm obligation collapses to the changed packages' own
// tests (or-6f0q soundness invariant: prune only with a machine-checkable
// argument). Evaluated AFTER regressionForcedFull, so go.mod changes win; a
// testdata/ or non-Go file fails the suffix test and keeps the blast path.
// Rename safety: changedPaths lists BOTH sides of a rename, so a prod→test
// rename includes the deleted .go path and never classifies as test-only.
func testOnlyChange(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if !strings.HasSuffix(p, "_test.go") {
			return false
		}
	}
	return true
}
