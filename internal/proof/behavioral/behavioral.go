// Package behavioral is the behavioral proof mode (or-60u, PRD Phase E8). The
// proof domain copies the artifact into a PROOF-CONTROLLED build dir (never the
// generator's worktree), adds the harness-authored corpus, and runs the tests
// independently. The generating agent never sees this dir or the corpus.
package behavioral

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
)

// Prove runs the synthesized behavioral corpus against the artifact in
// artifactDir and returns the behavioral ModeResult. corpusDirOut, if non-nil,
// receives the proof-controlled build dir path (for isolation assertions); the
// dir is otherwise removed. The mutation gate runs at the Standard threshold;
// tier-calibrated callers use ProveWithThreshold (or-v9f.11).
func Prove(ctx context.Context, artifactDir string, c testsynth.Contract, corpusDirOut *string) (truthalign.ModeResult, error) {
	return ProveWithThreshold(ctx, artifactDir, c, corpusDirOut, reliabilitytier.MutationThreshold(reliabilitytier.Standard))
}

// ProveWithThreshold is Prove with the mutation-score bar supplied by the caller
// — the classified reliability tier finally reaches the gate (or-v9f.11): a
// critical artifact is held to 0.9, a throwaway to 0.
func ProveWithThreshold(ctx context.Context, artifactDir string, c testsynth.Contract, corpusDirOut *string, mutationThreshold float64) (truthalign.ModeResult, error) {
	// or-4y7.5: the corpus author + test runner + mutation engine dispatch by the
	// contract's language ("" → go, verbatim). An unregistered language refuses —
	// running the Go corpus over non-Go code would be a meaningless proof.
	pv := proverFor(c.Language)
	if pv == nil {
		return truthalign.ModeResult{}, fmt.Errorf("behavioral: no prover registered for language %q", c.Language)
	}
	proofDir, err := os.MkdirTemp("", "orion-proof-*")
	if err != nil {
		return truthalign.ModeResult{}, fmt.Errorf("proof dir: %w", err)
	}
	keep := corpusDirOut != nil
	if !keep {
		defer func() { _ = os.RemoveAll(proofDir) }()
	} else {
		*corpusDirOut = proofDir
	}

	// Copy the artifact RECURSIVELY into the proof-controlled dir (or-v9f.23:
	// multi-file/multi-package artifacts; unit cases target subpackages).
	if err := stageTree(artifactDir, proofDir); err != nil {
		return truthalign.ModeResult{}, fmt.Errorf("stage artifact: %w", err)
	}
	// Write the harness-authored corpus (held by the proof domain; authored by
	// the language's prover — the generating agent never sees this dir).
	corpusFiles, serr := pv.SynthesizeCorpus(c, proofDir)
	if serr != nil {
		return truthalign.ModeResult{}, fmt.Errorf("corpus synthesis: %w", serr)
	}
	for rel, content := range corpusFiles {
		p := filepath.Join(proofDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return truthalign.ModeResult{}, fmt.Errorf("corpus dir for %s: %w", rel, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return truthalign.ModeResult{}, fmt.Errorf("write corpus %s: %w", rel, err)
		}
	}

	// Run the tests independently (the baseline) INSIDE the proof sandbox (network
	// + filesystem isolated) so the generated code under test cannot read host
	// secrets or reach the network.
	output, code, err := pv.RunTests(ctx, proofDir)
	if err != nil {
		return truthalign.ModeResult{}, fmt.Errorf("behavioral exec: %w", err)
	}
	pass := code == 0

	metrics := map[string]float64{"run_count": 1, "mutation_score": 0}
	obligations := parseObligations(output) // per-case executed/passed from markers
	inconclusive := false
	if pass {
		// Behavioral quality gate: the corpus must KILL behavior-changing mutants
		// (green coverage is a vanity metric; mutation score is the signal). The
		// engine is the prover's; the GATE below is shared — a language without a
		// mutation engine returns total==0 and reads UNMEASURED, never a pass.
		var unitPkgs []string
		seenPkg := map[string]bool{}
		for _, cs := range c.Cases {
			if cs.Kind == spec.KindUnit && cs.Unit != nil && !seenPkg[cs.Unit.Pkg] {
				seenPkg[cs.Unit.Pkg] = true
				unitPkgs = append(unitPkgs, cs.Unit.Pkg)
			}
		}
		killed, total, mErr := pv.MutationScore(ctx, artifactDir, corpusFiles, c.Entry(), unitPkgs)
		if mErr == nil {
			metrics["mutation_score"] = MutationScoreValue(killed, total)
			var note string
			pass, inconclusive, note = mutationGate(pass, killed, total, mutationThreshold)
			if note != "" {
				output += "\n" + note
			}
		}
	}
	return truthalign.ModeResult{
		Mode:         "behavioral",
		Pass:         pass,
		Inconclusive: inconclusive,
		Output:       output,
		Metrics:      metrics,
		Obligations:  obligations,
	}, nil
}

// mutationGate is the deterministic mutation-score decision (or-v9f.11): below
// the tier threshold fails; ZERO applicable mutants is Inconclusive — the corpus
// quality is unmeasured, which must never read as a silent pass.
func mutationGate(pass bool, killed, total int, threshold float64) (bool, bool, string) {
	switch {
	case total == 0:
		return false, true, "mutation gate: no applicable mutants — corpus fault-catching quality is UNMEASURED (inconclusive, not a pass)"
	case MutationScoreValue(killed, total) < threshold:
		return false, false, fmt.Sprintf("mutation gate: score %.2f (%d/%d) below threshold %.2f — corpus is not fault-catching", MutationScoreValue(killed, total), killed, total, threshold)
	}
	return pass, false, ""
}

// parseObligations reads ORION_OBLIGATION_RUN/PASS:<caseID> markers from the
// `go test -v` output. RUN seen → executed; PASS seen → passed. A case test that
// panicked or whose build failed prints no markers → never executed (a coverage
// hole, not a silent pass).
func parseObligations(output string) map[string]truthalign.ObligationStatus {
	obs := map[string]truthalign.ObligationStatus{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if id, ok := strings.CutPrefix(line, "ORION_OBLIGATION_RUN:"); ok {
			st := obs[id]
			st.Executed = true
			obs[id] = st
		} else if id, ok := strings.CutPrefix(line, "ORION_OBLIGATION_PASS:"); ok {
			st := obs[id]
			st.Executed, st.Passed = true, true
			obs[id] = st
		}
	}
	return obs
}

// stageTree copies the artifact tree (Go sources, go.mod/go.sum, and asset
// files) into dst, skipping dot-dirs and any prior Orion corpus files.
func stageTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if strings.HasPrefix(name, "orion_") && strings.HasSuffix(name, "_test.go") {
			return nil // never re-stage a prior corpus
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
}
