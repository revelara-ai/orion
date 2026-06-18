// Package truthalign computes the proof Verdict from per-mode results — the
// adjudicator (or-60u, PRD Trace 4 / Orchestrator-Decision-Matrix reconciliation).
// It is SEPARATED from dispatch (Trust invariant 5): the Conductor invokes it and
// acts on the result; it never produces the verdict itself. The verdict is
// computed ONLY from harness-collected evidence, never an agent's EvidenceClaim.
//
// V2.0 starts single-mode (behavioral); the Converge signature already carries
// per-mode provenance and the Inconclusive state so empirical + hazard slot in
// (or-dzo and the hazard task) without changing the contract.
package truthalign

// Verdict is the converged proof outcome. It matches the Context Store's
// proofs.verdict CHECK domain.
type Verdict string

const (
	Accept       Verdict = "Accept"
	Reject       Verdict = "Reject"
	Inconclusive Verdict = "Inconclusive"
)

// ModeResult is one proof mode's outcome (behavioral | empirical | hazard), with
// the quantitative metrics that make degradation computable.
type ModeResult struct {
	Mode         string
	Pass         bool
	Inconclusive bool // e.g. high-variance / flaky → not a coin-flip Accept/Reject
	Output       string
	Metrics      map[string]float64
}

// Outcome is the converged verdict plus per-mode provenance and dissent.
type Outcome struct {
	Verdict    Verdict
	Modes      []ModeResult
	Dissenting []string // modes that did not pass
}

// Converge aggregates per-mode results into a single Verdict:
//   - no modes            → Inconclusive (nothing was proven)
//   - any mode Inconclusive → Inconclusive (flakiness is not a pass)
//   - all modes Pass      → Accept
//   - otherwise           → Reject (with the failing modes as dissent)
func Converge(modes ...ModeResult) Outcome {
	out := Outcome{Modes: modes}
	if len(modes) == 0 {
		out.Verdict = Inconclusive
		return out
	}
	anyInconclusive := false
	allPass := true
	for _, m := range modes {
		if m.Inconclusive {
			anyInconclusive = true
		}
		if !m.Pass {
			allPass = false
			out.Dissenting = append(out.Dissenting, m.Mode)
		}
	}
	switch {
	case anyInconclusive:
		out.Verdict = Inconclusive
	case allPass:
		out.Verdict = Accept
	default:
		out.Verdict = Reject
	}
	return out
}
