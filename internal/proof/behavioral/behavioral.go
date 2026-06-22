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

	"github.com/revelara-ai/orion/internal/proof/proofexec"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
)

// Prove runs the synthesized behavioral corpus against the artifact in
// artifactDir and returns the behavioral ModeResult. corpusDirOut, if non-nil,
// receives the proof-controlled build dir path (for isolation assertions); the
// dir is otherwise removed.
func Prove(ctx context.Context, artifactDir string, c testsynth.Contract, corpusDirOut *string) (truthalign.ModeResult, error) {
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

	// Copy the artifact (main.go + go.mod) into the proof-controlled dir.
	for _, f := range []string{"go.mod", "main.go"} {
		data, err := os.ReadFile(filepath.Join(artifactDir, f))
		if err != nil {
			return truthalign.ModeResult{}, fmt.Errorf("read artifact %s: %w", f, err)
		}
		if err := os.WriteFile(filepath.Join(proofDir, f), data, 0o644); err != nil {
			return truthalign.ModeResult{}, fmt.Errorf("stage artifact %s: %w", f, err)
		}
	}
	// Write the harness-authored corpus (held by the proof domain).
	corpus := testsynth.SynthesizeBehavioral(c)
	if err := os.WriteFile(filepath.Join(proofDir, "orion_behavioral_test.go"), []byte(corpus), 0o644); err != nil {
		return truthalign.ModeResult{}, fmt.Errorf("write corpus: %w", err)
	}

	// Run the tests independently (the baseline) INSIDE the proof sandbox (network
	// + filesystem isolated) so the generated code under test cannot read host
	// secrets or reach the network. -v so passing per-case obligation markers
	// (RUN/PASS) are not suppressed by `go test`.
	output, code, err := proofexec.GoToolchain(ctx, proofDir, "test", "-v", "./...")
	if err != nil {
		return truthalign.ModeResult{}, fmt.Errorf("behavioral exec: %w", err)
	}
	pass := code == 0

	metrics := map[string]float64{"run_count": 1, "mutation_score": 0}
	obligations := parseObligations(output) // per-case executed/passed from markers
	if pass {
		// Behavioral quality gate: the corpus must KILL behavior-changing mutants
		// (green coverage is a vanity metric; mutation score is the signal).
		killed, total, mErr := MutationScore(ctx, artifactDir, corpus, c.Entry())
		if mErr == nil {
			score := MutationScoreValue(killed, total)
			metrics["mutation_score"] = score
			if total > 0 && score < reliabilitytier.MutationThreshold(reliabilitytier.Standard) {
				pass = false
				output += fmt.Sprintf("\nmutation gate: score %.2f (%d/%d) below threshold — corpus is not fault-catching", score, killed, total)
			}
		}
	}
	return truthalign.ModeResult{
		Mode:        "behavioral",
		Pass:        pass,
		Output:      output,
		Metrics:     metrics,
		Obligations: obligations,
	}, nil
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
