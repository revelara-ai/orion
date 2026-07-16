package behavioral

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/revelara-ai/orion/internal/proof/proofexec"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
)

// LangProver is a language's behavioral-proof surface (or-4y7.5): it authors the
// proof-domain corpus, runs it in the sandbox, and measures its fault-catching
// quality (mutation score). Everything else in the mode is language-neutral and
// stays shared: staging, the ORION_OBLIGATION_RUN/PASS marker protocol
// (parseObligations), the mutation gate + tier thresholds, and the ModeResult
// shape. Resolved by Contract.Language; the Go prover is the default and wraps
// the V2.0 free functions verbatim.
//
// A language without a mutation engine returns (0, 0, nil) from MutationScore —
// the shared mutationGate reads that as UNMEASURED/Inconclusive, never a silent
// pass (the honest reduced-proof fallback).
type LangProver interface {
	Language() string
	// SynthesizeCorpus authors the proof-domain test corpus for the contract:
	// proofDir-relative file paths → contents (root behavioral corpus, support
	// files, in-package unit tests). proofDir already holds the staged artifact.
	SynthesizeCorpus(c testsynth.Contract, proofDir string) (map[string]string, error)
	// RunTests executes the corpus in the proof sandbox, returning the combined
	// output (carrying the obligation markers) and the process exit code.
	RunTests(ctx context.Context, proofDir string) (output string, exitCode int, err error)
	// MutationScore measures the corpus's fault-catching quality over the
	// artifact: killed/total behavior-changing mutants. total==0 = unmeasured.
	MutationScore(ctx context.Context, artifactDir string, corpusFiles map[string]string, entrySym string, unitPkgs []string) (killed, total int, err error)
}

var provers = map[string]LangProver{}

func registerProver(p LangProver) { provers[p.Language()] = p }

// proverFor resolves the behavioral prover for a language ("" → go). An
// unregistered language returns nil — Prove refuses rather than running the Go
// corpus over non-Go code.
func proverFor(language string) LangProver {
	if language == "" {
		language = "go"
	}
	return provers[language]
}

// goProver is the default: the V2.0 Go corpus synthesis, go-test execution, and
// string+AST mutation engine, verbatim.
type goProver struct{}

func (goProver) Language() string { return "go" }

func (goProver) SynthesizeCorpus(c testsynth.Contract, proofDir string) (map[string]string, error) {
	files := map[string]string{"orion_behavioral_test.go": testsynth.SynthesizeBehavioral(c)}
	// or-v9f.3: exec cases assert through the embedded casecheck oracle — the
	// IDENTICAL semantics the empirical prober compiles in (§4.1).
	for name, content := range testsynth.SynthesizeSupportFiles(c) {
		files[name] = content
	}
	// or-v9f.23: unit cases become IN-PACKAGE obligation tests (restart-narrowed
	// cases run only in the empirical channel).
	unitFiles, err := testsynth.SynthesizeUnitTests(c.Cases, proofDir)
	if err != nil {
		return nil, fmt.Errorf("unit synthesis: %w", err)
	}
	for rel, content := range unitFiles {
		files[filepath.ToSlash(rel)] = content
	}
	return files, nil
}

func (goProver) RunTests(ctx context.Context, proofDir string) (string, int, error) {
	// -v so passing per-case obligation markers (RUN/PASS) are not suppressed.
	return proofexec.GoToolchain(ctx, proofDir, "test", "-v", "./...")
}

func (goProver) MutationScore(ctx context.Context, artifactDir string, corpusFiles map[string]string, entrySym string, unitPkgs []string) (int, int, error) {
	return MutationScoreFiles(ctx, artifactDir, corpusFiles, entrySym, unitPkgs)
}

func init() { registerProver(goProver{}) }
