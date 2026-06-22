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
// it survive.
type mutant struct {
	name string
	old  string
	new  string
}

// httpStringMutants are symbol-independent string mutations of the generated Go
// HTTP service that alter observable behavior the ResponseContract pins.
var httpStringMutants = []mutant{
	{"json-field-rename", `"time"`, `"t1me"`},
	{"json-content-type", `"application/json"`, `"application/octet-stream"`},
	{"text-content-type", `"text/plain; charset=utf-8"`, `"application/octet-stream"`},
}

// mutantsFor returns the behavior-changing mutants for an artifact whose DECLARED
// behavioral entry symbol is entry. The status-500 mutant targets that declared
// handler signature rather than a hardwired "handleTime", so the mutation gate
// generalizes with the contract (or-ciq).
func mutantsFor(entry string) []mutant {
	sig := fmt.Sprintf("func %s(w http.ResponseWriter, r *http.Request) {", entry)
	out := append([]mutant(nil), httpStringMutants...)
	out = append(out, mutant{"status-500", sig, sig + "\n\tw.WriteHeader(500)"})
	return out
}

// MutationScore mutates the artifact and runs the corpus against each applicable
// mutant. Returns killed and total (applicable) counts. A mutant is "killed" when
// the corpus fails on it. The caller should have verified the corpus passes on
// the unmutated artifact first.
func MutationScore(ctx context.Context, artifactDir, corpusSource, entrySym string) (killed, total int, err error) {
	mainSrc, err := os.ReadFile(filepath.Join(artifactDir, "main.go"))
	if err != nil {
		return 0, 0, err
	}
	gomod, err := os.ReadFile(filepath.Join(artifactDir, "go.mod"))
	if err != nil {
		return 0, 0, err
	}
	for _, m := range mutantsFor(entrySym) {
		if !strings.Contains(string(mainSrc), m.old) {
			continue // mutant not applicable to this artifact
		}
		total++
		mutated := strings.Replace(string(mainSrc), m.old, m.new, 1)
		dir, e := os.MkdirTemp("", "orion-mutant-*")
		if e != nil {
			return killed, total, e
		}
		_ = os.WriteFile(filepath.Join(dir, "go.mod"), gomod, 0o644)
		_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte(mutated), 0o644)
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
