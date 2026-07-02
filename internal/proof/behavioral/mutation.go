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
	mainSrc, err := os.ReadFile(filepath.Join(artifactDir, "main.go"))
	if err != nil {
		return 0, 0, err
	}
	gomod, err := os.ReadFile(filepath.Join(artifactDir, "go.mod"))
	if err != nil {
		return 0, 0, err
	}

	var candidates []mutant
	for _, m := range mutantsFor(entrySym) {
		if !strings.Contains(string(mainSrc), m.old) {
			continue // string mutant not applicable to this artifact
		}
		m.source = strings.Replace(string(mainSrc), m.old, m.new, 1)
		candidates = append(candidates, m)
	}
	// AST mutants fill in ONLY when no contract-aware mutant applies (e.g. a
	// non-HTTP artifact, which previously no-opped to a silent pass). They are
	// NOT mixed into an HTTP artifact's score: contract-irrelevant sites carry
	// equivalent/unobservable mutants that dilute the score and fail honest
	// corpora — the gate must measure the corpus, not the artifact's incidental
	// mutability.
	if len(candidates) == 0 {
		candidates = astMutants(string(mainSrc), astMutantCap)
	}

	for _, m := range candidates {
		dir, e := os.MkdirTemp("", "orion-mutant-*")
		if e != nil {
			return killed, total, e
		}
		_ = os.WriteFile(filepath.Join(dir, "go.mod"), gomod, 0o644)
		_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte(m.source), 0o644)
		// Compile-validity first (no corpus in the dir yet): a mutant that does not
		// compile is not a behavior change and must not count either way.
		if _, code, buildErr := proofexec.GoToolchain(ctx, dir, "build", "./..."); buildErr != nil || code != 0 {
			_ = os.RemoveAll(dir)
			if buildErr != nil {
				return killed, total, buildErr
			}
			continue // discarded: invalid mutant
		}
		total++
		_ = os.WriteFile(filepath.Join(dir, "orion_behavioral_test.go"), []byte(corpusSource), 0o644)
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

// MutationScoreValue is killed/total (0 when no applicable mutants).
func MutationScoreValue(killed, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(killed) / float64(total)
}
