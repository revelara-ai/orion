// Package behavioral is the behavioral proof mode (or-60u, PRD Phase E8). The
// proof domain copies the artifact into a PROOF-CONTROLLED build dir (never the
// generator's worktree), adds the harness-authored corpus, and runs the tests
// independently. The generating agent never sees this dir or the corpus.
package behavioral

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
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
		defer os.RemoveAll(proofDir)
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

	// Run the tests independently.
	cmd := exec.CommandContext(ctx, "go", "test", "./...")
	cmd.Dir = proofDir
	cmd.Env = append(os.Environ(), "GOFLAGS=") // inherit toolchain/cache
	out, err := cmd.CombinedOutput()
	pass := err == nil
	return truthalign.ModeResult{
		Mode:    "behavioral",
		Pass:    pass,
		Output:  string(out),
		Metrics: map[string]float64{"run_count": 1},
	}, nil
}
