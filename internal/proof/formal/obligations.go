package formal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/proof/safeenv"
)

// Obligation binds a verified design invariant to the behavioral test(s) that
// enforce it on the CODE — the refinement chain (or-56c.3). The binding lives
// in the model file itself as a `# obligation:` line, so the human ratifies
// the invariant AND its enforcement together.
type Obligation struct {
	Invariant  string
	Packages   []string
	RunPattern string
}

var obligationRe = regexp.MustCompile(`(?m)^#\s*obligation:\s*(\S+)\s*->\s*go test\s+(.+?)\s+-run\s+(\S+)\s*$`)
var assertionRe = regexp.MustCompile(`(?m)^always assertion\s+([A-Za-z0-9_]+)\s*:`)

// CompileObligations parses a ratified model's invariant→test bindings and
// verifies the binding is TOTAL both ways: every `always assertion` must carry
// at least one obligation (an unbound invariant makes the design proof
// decorative), and every obligation must name an invariant the model asserts
// (a dangling binding is a stale typo).
func CompileObligations(modelPath string) ([]Obligation, error) {
	b, err := os.ReadFile(modelPath)
	if err != nil {
		return nil, fmt.Errorf("read model: %w", err)
	}
	src := string(b)

	var obs []Obligation
	bound := map[string]bool{}
	for _, m := range obligationRe.FindAllStringSubmatch(src, -1) {
		o := Obligation{
			Invariant:  m[1],
			Packages:   strings.Fields(m[2]),
			RunPattern: strings.Trim(m[3], `'"`),
		}
		obs = append(obs, o)
		bound[o.Invariant] = true
	}

	asserted := map[string]bool{}
	for _, m := range assertionRe.FindAllStringSubmatch(src, -1) {
		asserted[m[1]] = true
	}
	// Zero applicable invariants = Inconclusive, never a silent pass (or-56c.4):
	// a fired gate whose model asserts nothing has proven nothing.
	if len(asserted) == 0 {
		return nil, fmt.Errorf("model %s declares no invariants (no `always assertion`) — the design proof is Inconclusive, not a pass", filepath.Base(modelPath))
	}

	var unbound, dangling []string
	for inv := range asserted {
		if !bound[inv] {
			unbound = append(unbound, inv)
		}
	}
	for inv := range bound {
		if !asserted[inv] {
			dangling = append(dangling, inv)
		}
	}
	sort.Strings(unbound)
	sort.Strings(dangling)
	if len(unbound) > 0 {
		return nil, fmt.Errorf("model %s: invariant(s) without a behavioral obligation (the design proof would be decorative): %s — add `# obligation: <Invariant> -> go test <pkgs> -run <pattern>`", filepath.Base(modelPath), strings.Join(unbound, ", "))
	}
	if len(dangling) > 0 {
		return nil, fmt.Errorf("model %s: obligation(s) bound to invariants the model does not assert: %s", filepath.Base(modelPath), strings.Join(dangling, ", "))
	}
	return obs, nil
}

// EnforceRefinement proves the refinement chain on one model: (1) the checker
// verifies the design invariants; (2) every invariant's obligation resolves to
// at least one real test function (the or-crmw rot class fails loud, never
// silently); (3) the obligation tests PASS against the code, run under safeenv
// (host module cache — these are the repo's own tests, the regression-gate
// posture, not the hermetic proofexec one).
func EnforceRefinement(ctx context.Context, repoRoot, modelPath string, checker Runner) error {
	res, err := checker.Check(ctx, modelPath)
	if err != nil {
		return fmt.Errorf("design proof: %w", err)
	}
	if res.Skipped != "" {
		return fmt.Errorf("design proof unavailable: %s", res.Skipped)
	}
	if !res.Passed {
		return fmt.Errorf("design proof FAILED (invariant %q, deadlock=%v) — nothing to refine", res.Invariant, res.Deadlock)
	}
	obs, err := CompileObligations(modelPath)
	if err != nil {
		return err
	}
	for _, o := range obs {
		re, rerr := regexp.Compile(o.RunPattern)
		if rerr != nil {
			return fmt.Errorf("obligation %s: -run pattern %q does not compile: %w", o.Invariant, o.RunPattern, rerr)
		}
		if !patternResolves(repoRoot, o.Packages, re) {
			return fmt.Errorf("obligation %s: -run %q matches no test function under %v — the invariant is verified but UNENFORCED on the code", o.Invariant, o.RunPattern, o.Packages)
		}
		args := append([]string{"test"}, o.Packages...)
		args = append(args, "-run", o.RunPattern, "-count=1")
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir = repoRoot
		cmd.Env = safeenv.Build()
		if out, terr := cmd.CombinedOutput(); terr != nil {
			return fmt.Errorf("obligation %s: enforcement tests failed: %w\n%s", o.Invariant, terr, string(out))
		}
	}
	return nil
}

var testFuncRe = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\s*\(`)

// patternResolves reports whether the -run pattern matches at least one test
// function under the package patterns (./dir/... walks; ./... walks the repo,
// skipping archive/).
func patternResolves(root string, pkgs []string, re *regexp.Regexp) bool {
	for _, pat := range pkgs {
		rel := strings.TrimPrefix(pat, "./")
		rel = strings.TrimSuffix(rel, "...")
		rel = strings.TrimSuffix(rel, "/")
		found := false
		_ = filepath.WalkDir(filepath.Join(root, rel), func(path string, d os.DirEntry, err error) error {
			if err != nil || found {
				return nil
			}
			if d.IsDir() {
				if n := d.Name(); n == ".git" || n == "archive" {
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
			for _, m := range testFuncRe.FindAllStringSubmatch(string(b), -1) {
				if re.MatchString(m[1]) {
					found = true
					return filepath.SkipAll
				}
			}
			return nil
		})
		if found {
			return true
		}
	}
	return false
}
