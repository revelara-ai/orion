package behavioral

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/proof/proofexec"
)

// mutant is a deliberate behavior-changing edit to the artifact. A fault-catching
// corpus must KILL it (the test fails on the mutant); a tautological corpus lets
// it survive. String mutants carry old/new (applied by substitution, skipped when
// not applicable); AST mutants (or-v9f.11) carry the full mutated source.
type mutant struct {
	name   string
	old    string
	new    string
	source string
}

// httpStringMutants are symbol-independent string mutations of the generated Go
// HTTP service that alter observable behavior the ResponseContract pins.
var httpStringMutants = []mutant{
	{name: "json-field-rename", old: `"time"`, new: `"t1me"`},
	{name: "json-content-type", old: `"application/json"`, new: `"application/octet-stream"`},
	{name: "text-content-type", old: `"text/plain; charset=utf-8"`, new: `"application/octet-stream"`},
}

// mutantsFor returns the behavior-changing mutants for an artifact whose DECLARED
// behavioral entry symbol is entry. The status-500 mutant targets that declared
// handler signature rather than a hardwired "handleTime", so the mutation gate
// generalizes with the contract (or-ciq).
func mutantsFor(entry string) []mutant {
	sig := fmt.Sprintf("func %s(w http.ResponseWriter, r *http.Request) {", entry)
	out := append([]mutant(nil), httpStringMutants...)
	out = append(out, mutant{name: "status-500", old: sig, new: sig + "\n\tw.WriteHeader(500)"})
	return out
}

// MutationScore mutates the artifact and runs the corpus against each applicable
// mutant. Returns killed and total (applicable) counts. A mutant is "killed" when
// the corpus fails on it. The caller should have verified the corpus passes on
// the unmutated artifact first.
//
// or-v9f.11: contract-aware string mutants are joined by AST mutants (comparison
// flips, boolean flips, arithmetic swaps), capped at astMutantCap total. Every
// mutant is COMPILE-CHECKED first: an invalid mutant is discarded from total —
// previously a non-compiling mutant's failing test run counted as killed,
// inflating the score.
func MutationScore(ctx context.Context, artifactDir, corpusSource, entrySym string) (killed, total int, err error) {
	return MutationScoreFiles(ctx, artifactDir, map[string]string{"orion_behavioral_test.go": corpusSource}, entrySym, nil)
}

// MutationScoreFiles is MutationScore with the FULL corpus placement (root
// corpus + support files + in-package unit tests) and mutation targets drawn
// from every file a unit case exercises (or-v9f.23): library artifacts are
// mutation-scored on their case packages, not just main.go.
func MutationScoreFiles(ctx context.Context, artifactDir string, corpusFiles map[string]string, entrySym string, unitPkgs []string) (killed, total int, err error) {
	mainSrc, err := os.ReadFile(filepath.Join(artifactDir, "main.go"))
	if err != nil {
		return 0, 0, err
	}

	type target struct {
		rel string
		m   mutant
	}
	var candidates []target
	for _, m := range mutantsFor(entrySym) {
		if !strings.Contains(string(mainSrc), m.old) {
			continue // string mutant not applicable to this artifact
		}
		m.source = strings.Replace(string(mainSrc), m.old, m.new, 1)
		candidates = append(candidates, target{rel: "main.go", m: m})
	}
	// AST mutants fill in ONLY where no contract-aware mutant applies: the
	// artifact's main.go when nothing matched there, and every unit-case
	// package's sources (the library surface the unit corpus must pin). The gate
	// measures the corpus, never the artifact's incidental mutability — so AST
	// mutants never mix into an HTTP artifact's contract-mutant score.
	if len(candidates) == 0 {
		for _, m := range astMutants(string(mainSrc), astMutantCap) {
			candidates = append(candidates, target{rel: "main.go", m: m})
		}
	}
	for _, pkg := range unitPkgs {
		entries, rerr := os.ReadDir(filepath.Join(artifactDir, pkg))
		if rerr != nil {
			continue // the fast-tier diagnostic already reports missing packages
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			rel := filepath.Join(pkg, e.Name())
			src, rerr := os.ReadFile(filepath.Join(artifactDir, rel))
			if rerr != nil {
				continue
			}
			if len(candidates) >= astMutantCap {
				break
			}
			for _, m := range astMutants(string(src), astMutantCap-len(candidates)) {
				candidates = append(candidates, target{rel: rel, m: m})
			}
		}
	}

	for _, t := range candidates {
		dir, e := os.MkdirTemp("", "orion-mutant-*")
		if e != nil {
			return killed, total, e
		}
		if e := copyTree(artifactDir, dir); e != nil {
			_ = os.RemoveAll(dir)
			return killed, total, e
		}
		_ = os.WriteFile(filepath.Join(dir, t.rel), []byte(t.m.source), 0o644)
		// Compile-validity first (no corpus yet): a mutant that does not compile
		// is not a behavior change and must not count either way.
		if _, code, buildErr := proofexec.GoToolchain(ctx, dir, "build", "./..."); buildErr != nil || code != 0 {
			_ = os.RemoveAll(dir)
			if buildErr != nil {
				return killed, total, buildErr
			}
			continue // discarded: invalid mutant
		}
		total++
		for rel, content := range corpusFiles {
			p := filepath.Join(dir, rel)
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.WriteFile(p, []byte(content), 0o644)
		}
		// Run the mutant's corpus inside the proof sandbox (mutated generated code
		// never sees host secrets and cannot reach the network).
		_, code, execErr := proofexec.GoToolchain(ctx, dir, "test", "./...")
		_ = os.RemoveAll(dir)
		if execErr != nil {
			return killed, total, execErr
		}
		if code != 0 {
			killed++ // corpus caught the mutant
		}
	}
	return killed, total, nil
}

// copyTree recursively copies the artifact (dot-dirs and prior corpora skipped).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil || rel == "." {
			return rerr
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if strings.HasPrefix(name, "orion_") && strings.HasSuffix(name, "_test.go") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
}

// MutationScoreValue is killed/total (0 when no applicable mutants).
func MutationScoreValue(killed, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(killed) / float64(total)
}
