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
	"github.com/revelara-ai/orion/internal/proof/empirical"
	"github.com/revelara-ai/orion/internal/proof/hazard"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// ModeReport is one mode's result plus its mode-specific detail (persisted as the
// proof row's detail JSON, e.g. empirical {port_open, response_contract_satisfied}).
type ModeReport struct {
	Result truthalign.ModeResult
	Detail map[string]any
}

// Report is the converged verdict plus per-mode reports.
type Report struct {
	Outcome truthalign.Outcome
	Modes   []ModeReport
}

// ProveBehavioral synthesizes the behavioral corpus from the contract, runs it
// against the artifact, and converges a (single-mode) Verdict.
func ProveBehavioral(ctx context.Context, artifactDir string, c testsynth.Contract) (truthalign.Outcome, error) {
	mr, err := behavioral.Prove(ctx, artifactDir, c, nil)
	if err != nil {
		return truthalign.Outcome{}, err
	}
	return truthalign.Converge(mr), nil
}

// Prove runs behavioral AND empirical proof and converges. A Verdict is Accept
// only when BOTH modes pass — so a passing-tests-but-failing-probe artifact does
// not converge to Accept.
func Prove(ctx context.Context, artifactDir string, c testsynth.Contract) (Report, error) {
	bmr, err := behavioral.Prove(ctx, artifactDir, c, nil)
	if err != nil {
		return Report{}, err
	}
	emr, pr, err := empirical.Prove(ctx, artifactDir, c)
	if err != nil {
		return Report{}, err
	}
	outcome := truthalign.Converge(bmr, emr)
	return Report{
		Outcome: outcome,
		Modes: []ModeReport{
			{Result: bmr},
			{Result: emr, Detail: map[string]any{
				"port_open":                   pr.PortOpen,
				"response_contract_satisfied": pr.ResponseContractSatisfied,
				"detail":                      pr.Detail,
			}},
		},
	}, nil
}

// ProveAll runs all three modes (behavioral + empirical + hazard) against the
// artifact and the ratified STPA model, and converges requiring all three. This
// is the full credibility-core verdict.
func ProveAll(ctx context.Context, artifactDir string, c testsynth.Contract, model stpa.Model) (Report, error) {
	bmr, err := behavioral.Prove(ctx, artifactDir, c, nil)
	if err != nil {
		return Report{}, err
	}
	emr, pr, err := empirical.Prove(ctx, artifactDir, c)
	if err != nil {
		return Report{}, err
	}
	hmr, hr, err := hazard.Prove(ctx, artifactDir, model)
	if err != nil {
		return Report{}, err
	}
	outcome := truthalign.ConvergeFull(bmr, emr, hmr)
	return Report{
		Outcome: outcome,
		Modes: []ModeReport{
			{Result: bmr},
			{Result: emr, Detail: map[string]any{
				"port_open":                   pr.PortOpen,
				"response_contract_satisfied": pr.ResponseContractSatisfied,
				"detail":                      pr.Detail,
			}},
			{Result: hmr, Detail: map[string]any{
				"ucas_considered":   hr.UCAsConsidered,
				"uncontrolled_ucas": hr.UncontrolledUCAs,
				"accepted_gaps":     hr.AcceptedGaps,
				"control_actions":   hr.ControlActions,
			}},
		},
	}, nil
}
