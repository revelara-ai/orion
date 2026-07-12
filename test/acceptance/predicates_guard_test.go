package acceptance

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// deferredPredicates are kindGoTest predicates whose -run target intentionally
// matches NO test yet: the surface is unbuilt, red-by-design, awaiting its epic.
// Each entry MUST carry a tracking id + reason. Like deferredOrphans, this list
// is a RATCHET (or-crmw): an entry whose -run pattern begins matching a real
// test fails the guard (forcing its removal and an honest re-score), and an
// UNLISTED predicate matching zero tests fails outright — so a test rename can
// never silently turn a passing predicate red again (the or-xe7.3 incident:
// TestLoopProceedsWhenPolarisUnreachable was renamed and the predicate read as
// deferred-red for 11 days).
var deferredPredicates = map[string]string{
	"memory/memory-store / context-store divergence detected": "or-gb1: divergence detector unbuilt (V2 memory-hardening)",
	"memory/LTM promotion crash-safe, no corruption":          "or-gb1: no crash-mid-promotion test yet (V2 memory-hardening)",
	"self-evolution/self-evolution regression gate":           "or-gb1.5: SkillEval pre-activation gate unbuilt",
	"self-evolution/developer-scoped LTM redacts project literals": "or-gb1.6: project-scoped memory + redaction unbuilt",
	"integration/rebase on moved head triggers re-proof":           "or-dka: exercised by TestOverlappingIntegrationsSerialize but not asserted; needs a moved-head re-proof assertion",
	"integration/rebase conflict dispatches resolver or escalates": "or-dka: resolver dispatch unbuilt (conflict containment IS proven: TestIntegrateConflictLeavesHeadUntouched)",
	"integration/stale integration lock recovery on restart":       "or-dka: restart recovery unbuilt",
	"integration/resolved merge proof covers all original obligations": "or-dka: resolver flow unbuilt",
	"packages/installed skill grants capped to role ceiling":           "or-ykz.2: package/extension system unbuilt",
	"packages/eval harness rejects non-deterministic predicate":        "or-gb1.5: eval harness unbuilt",
	"polaris/evidence write is idempotent on retry":                    "or-lrr: Polaris write-back deferred",
	"polaris/knowledge contribution contains no raw code or paths":     "or-lrr: Polaris write-back deferred",
}

// TestPredicateRunTargetsResolve is the anti-rot guard for the acceptance
// harness itself: every kindGoTest predicate's -run pattern must match at least
// one test function in its target packages, or be on deferredPredicates.
func TestPredicateRunTargetsResolve(t *testing.T) {
	root := repoRootDir(t)
	for _, p := range predicates {
		if p.Kind != kindGoTest {
			continue
		}
		key := p.Category + "/" + p.Name
		pkgs, runPat := parseGoTestScript(t, p.Script)
		if runPat == "" {
			t.Errorf("%s: could not parse a -run pattern from %q", key, p.Script)
			continue
		}
		re, err := regexp.Compile(runPat)
		if err != nil {
			t.Errorf("%s: -run pattern %q does not compile: %v", key, runPat, err)
			continue
		}
		matched := false
		for _, name := range testFuncsUnder(t, root, pkgs) {
			if re.MatchString(name) {
				matched = true
				break
			}
		}
		reason, deferred := deferredPredicates[key]
		switch {
		case !matched && !deferred:
			t.Errorf("%s: -run %q matches NO test function under %v — rename rot (fix the pattern) or add to deferredPredicates with a tracking id", key, runPat, pkgs)
		case matched && deferred:
			t.Errorf("%s: deferred (%s) but -run %q now MATCHES a real test — remove the deferred entry and re-score", key, reason, runPat)
		}
	}
}

// parseGoTestScript extracts the ./... package patterns and the -run value from
// a `go test <pkgs> -run <pattern>` predicate script (quotes stripped).
func parseGoTestScript(t *testing.T, script string) (pkgs []string, runPat string) {
	t.Helper()
	fields := strings.Fields(script)
	for i, f := range fields {
		if strings.HasPrefix(f, "./") {
			pkgs = append(pkgs, f)
		}
		if f == "-run" && i+1 < len(fields) {
			runPat = strings.Trim(fields[i+1], `'"`)
			// Shell interpolation in loop-style predicates ("TestMissing${d}Dimension…"):
			// resolvability is checked with the variable as a wildcard.
			runPat = regexp.MustCompile(`\$\{?\w+\}?`).ReplaceAllString(runPat, `.*`)
		}
	}
	return pkgs, runPat
}

// testFuncsUnder returns every `func TestXxx(` name in _test.go files under the
// given package patterns (./dir/... walks recursively; ./... walks the repo).
func testFuncsUnder(t *testing.T, root string, pkgs []string) []string {
	t.Helper()
	funcRe := regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\s*\(`)
	var names []string
	seen := map[string]bool{}
	for _, pat := range pkgs {
		rel := strings.TrimPrefix(pat, "./")
		rel = strings.TrimSuffix(rel, "...")
		rel = strings.TrimSuffix(rel, "/")
		dir := filepath.Join(root, rel)
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				base := d.Name()
				if base == ".git" || base == "archive" || base == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			for _, m := range funcRe.FindAllStringSubmatch(string(b), -1) {
				if !seen[m[1]] {
					seen[m[1]] = true
					names = append(names, m[1])
				}
			}
			return nil
		})
	}
	return names
}

// repoRootDir finds the module root by walking up to go.mod.
func repoRootDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from the test dir")
		}
		dir = parent
	}
}
