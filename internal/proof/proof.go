// Package proof is the Proof Harness (or-60u, PRD Trace 4). It runs the proof
// modes and converges a Verdict from harness-collected evidence — never from an
// agent's EvidenceClaim. V2.0 ships the behavioral mode; empirical (Lookout) and
// hazard (STPA) modes converge alongside it in later tasks.
//
// Manifesto: no agent grades its own homework — the corpus is authored by the
// proof domain (testsynth) and is unreachable from the generation sandbox.
package proof

import (
	"context"

	"github.com/revelara-ai/orion/internal/proof/behavioral"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// ProveBehavioral synthesizes the behavioral corpus from the contract, runs it
// against the artifact, and converges a (single-mode) Verdict.
func ProveBehavioral(ctx context.Context, artifactDir string, c testsynth.Contract) (truthalign.Outcome, error) {
	mr, err := behavioral.Prove(ctx, artifactDir, c, nil)
	if err != nil {
		return truthalign.Outcome{}, err
	}
	return truthalign.Converge(mr), nil
}
