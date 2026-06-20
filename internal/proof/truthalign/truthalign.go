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

// ObligationStatus is one behavioral case's per-mode execution record. Executed
// distinguishes "ran and failed" from "never ran" (a coverage hole) — the
// distinction the Phase-3 ObligationGate needs to escalate vs reject.
type ObligationStatus struct {
	Executed bool
	Passed   bool
}

// ModeResult is one proof mode's outcome (behavioral | empirical | hazard), with
// the quantitative metrics that make degradation computable.
type ModeResult struct {
	Mode         string
	Pass         bool
	Inconclusive bool // e.g. high-variance / flaky → not a coin-flip Accept/Reject
	Output       string
	Metrics      map[string]float64
	// Obligations is per-case (caseID → status) for modes that execute behavioral
	// cases (behavioral, empirical). Nil for modes that don't (hazard).
	Obligations map[string]ObligationStatus
}

// Outcome is the converged verdict plus per-mode provenance and dissent.
type Outcome struct {
	Verdict    Verdict
	Modes      []ModeResult
	Dissenting []string // modes that did not pass
}

// RequiredModes are the three proof modes a full V2.0 verdict requires.
var RequiredModes = []string{"behavioral", "empirical", "hazard"}

// ConvergeFull is Converge with the requirement that ALL three modes are present.
// A missing mode yields Inconclusive (the loop is not fully proven), never a
// 2-of-3 Accept.
func ConvergeFull(modes ...ModeResult) Outcome {
	present := map[string]bool{}
	for _, m := range modes {
		present[m.Mode] = true
	}
	for _, r := range RequiredModes {
		if !present[r] {
			out := Converge(modes...)
			out.Verdict = Inconclusive
			out.Dissenting = append(out.Dissenting, "missing:"+r)
			return out
		}
	}
	return Converge(modes...)
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
