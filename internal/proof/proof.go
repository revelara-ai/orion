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
	"github.com/revelara-ai/orion/internal/proof/diagnostics"
	"github.com/revelara-ai/orion/internal/proof/empirical"
	"github.com/revelara-ai/orion/internal/proof/hazard"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
)

// ModeReport is one mode's result plus its mode-specific detail (persisted as the
// proof row's detail JSON, e.g. empirical {port_open, response_contract_satisfied}).
type ModeReport struct {
	Result truthalign.ModeResult
	Detail map[string]any
}

// ObligationResult is a behavioral case's aggregated proof outcome across modes:
// Executed (ran in >=1 mode), Passed (ran and passed in every mode that ran it),
// and the modes that passed it. The Phase-3 ObligationGate consumes this to
// require every declared case to have actually run and passed.
type ObligationResult struct {
	Executed bool
	Passed   bool
	Modes    []string
}

// Report is the converged verdict plus per-mode reports and per-case obligations.
type Report struct {
	Outcome           truthalign.Outcome
	Modes             []ModeReport
	ObligationResults map[string]ObligationResult
}

// PresentModes lists the proof modes that actually RAN, in report order — the
// deployment bar consumes this instead of a hardcoded list, so a tier that
// requires full convergence can genuinely refuse a partial proof (or-v9f.13).
func (r Report) PresentModes() []string {
	out := make([]string, 0, len(r.Modes))
	for _, m := range r.Modes {
		if m.Result.Mode != "" {
			out = append(out, m.Result.Mode)
		}
	}
	return out
}

// aggregateObligations merges per-mode case statuses: a case is Executed if any
// mode ran it, and Passed only if it ran and passed in EVERY mode that ran it (a
// never-run case stays absent — a coverage hole the gate treats distinctly).
func aggregateObligations(modes []ModeReport) map[string]ObligationResult {
	type acc struct {
		exec, anyFail bool
		modes         []string
	}
	m := map[string]*acc{}
	for _, mr := range modes {
		for id, st := range mr.Result.Obligations {
			a := m[id]
			if a == nil {
				a = &acc{}
				m[id] = a
			}
			if st.Executed {
				a.exec = true
				if st.Passed {
					a.modes = append(a.modes, mr.Result.Mode)
				} else {
					a.anyFail = true
				}
			}
		}
	}
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]ObligationResult, len(m))
	for id, a := range m {
		out[id] = ObligationResult{Executed: a.exec, Passed: a.exec && !a.anyFail, Modes: a.modes}
	}
	return out
}

// EnforceObligations is the proof-time coverage gate (the or-y9d kill). Given the
// case IDs the spec REQUIRES, it downgrades the report's verdict so that a
// requirement which never ran → Inconclusive (a coverage hole; escalate, don't
// silently pass), and one that ran but failed → Reject. "Proven" can only mean
// every declared requirement was executed AND passed.
func EnforceObligations(required []string, r *Report) {
	var unexecuted, failed []string
	for _, id := range required {
		o, ok := r.ObligationResults[id]
		switch {
		case !ok || !o.Executed:
			unexecuted = append(unexecuted, id)
		case !o.Passed:
			failed = append(failed, id)
		}
	}
	if len(unexecuted) > 0 {
		r.Outcome.Verdict = truthalign.Inconclusive
		for _, id := range unexecuted {
			r.Outcome.Dissenting = append(r.Outcome.Dissenting, "unexecuted:"+id)
		}
	}
	if len(failed) > 0 && r.Outcome.Verdict == truthalign.Accept {
		r.Outcome.Verdict = truthalign.Reject
	}
	for _, id := range failed {
		r.Outcome.Dissenting = append(r.Outcome.Dissenting, "failed:"+id)
	}
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
	modes := []ModeReport{
		{Result: bmr},
		{Result: emr, Detail: map[string]any{
			"port_open":                   pr.PortOpen,
			"response_contract_satisfied": pr.ResponseContractSatisfied,
			"detail":                      pr.Detail,
		}},
	}
	return Report{Outcome: outcome, Modes: modes, ObligationResults: aggregateObligations(modes)}, nil
}

// ProveAll runs all three modes (behavioral + empirical + hazard) against the
// artifact and the ratified STPA model, and converges requiring all three. This
// is the full credibility-core verdict. The mutation gate runs at the Standard
// threshold; tier-calibrated callers use ProveAllWithThreshold (or-v9f.11).
func ProveAll(ctx context.Context, artifactDir string, c testsynth.Contract, model stpa.Model) (Report, error) {
	return ProveAllWithThreshold(ctx, artifactDir, c, model, reliabilitytier.MutationThreshold(reliabilitytier.Standard))
}

// ProveAllWithThreshold is ProveAll with the mutation-score bar supplied by the
// caller — the classified reliability tier reaches the behavioral gate.
func ProveAllWithThreshold(ctx context.Context, artifactDir string, c testsynth.Contract, model stpa.Model, mutationThreshold float64) (Report, error) {
	// Fast-feedback tier (cheapest first): a static check (compile + vet). If the
	// generated code does not compile, the behavioral/empirical/hazard modes cannot run
	// — return a Reject with the diagnostics immediately, skipping minutes of pointless
	// work. The refinement loop feeds these diagnostics straight back to the generator.
	if d := diagnostics.Check(ctx, artifactDir); !d.OK {
		mr := truthalign.ModeResult{Mode: "diagnostics", Pass: false, Output: d.Output}
		return Report{Outcome: truthalign.Converge(mr), Modes: []ModeReport{{Result: mr}}}, nil
	}

	bmr, err := behavioral.ProveWithThreshold(ctx, artifactDir, c, nil, mutationThreshold)
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
	modes := []ModeReport{
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
	}
	return Report{Outcome: outcome, Modes: modes, ObligationResults: aggregateObligations(modes)}, nil
}
