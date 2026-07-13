package contextengine

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Evidence-sufficiency gate, E2.5 (or-gb1.1, PRINCE's missing reflection
// loop): between assembling the ContextBundle (E2) and dispatching the
// generator (E3), decide CHEAPLY whether the evidence needed by the task's
// proof obligations is actually present — instead of discovering an
// insufficient bundle reactively via proof-reject-reloop, the most expensive
// possible point. Deterministic core: the caller derives `needs` (obligation
// inputs) from the ratified spec; the gate checks presence, re-recalls with
// a gap-focused query up to a bounded cycle count, and on exhaustion says
// NEEDS HUMAN — it never silently proceeds and never touches a proof
// verdict (the outcome enum has no verdict; trust invariants 1-3, 7 hold by
// construction: the gate reads ONLY the bundle + needs).

// SufficiencyOutcome is the E2.5 decision.
type SufficiencyOutcome string

const (
	Sufficient  SufficiencyOutcome = "sufficient"   // → dispatch (E3)
	NeedsRecall SufficiencyOutcome = "needs_recall" // → re-run E2 with the gap query
	NeedsHuman  SufficiencyOutcome = "needs_human"  // → escalation; do not silently proceed
)

// SufficiencyReport carries the decision + the named gaps + cycles spent.
type SufficiencyReport struct {
	Outcome SufficiencyOutcome
	Gaps    []string
	Cycles  int
}

// maxSufficiencyCycles is context.max_sufficiency_cycles (env override
// ORION_SUFFICIENCY_CYCLES); small by design — the gate is a guard, not a
// search loop.
func maxSufficiencyCycles() int {
	if v := os.Getenv("ORION_SUFFICIENCY_CYCLES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 10 {
			return n
		}
	}
	return 2
}

// CheckSufficiency is the deterministic presence check: every need must
// appear (case-insensitive) somewhere in the bundle's evidence — constraints,
// trusted, or quarantined untrusted items (quarantine bounds TRUST, not
// availability). Missing needs come back as named gaps.
func CheckSufficiency(b Bundle, needs []string) SufficiencyReport {
	var evidence strings.Builder
	for _, c := range b.Constraints {
		evidence.WriteString(c)
		evidence.WriteString("\n")
	}
	for _, it := range b.Trusted {
		evidence.WriteString(it.Content)
		evidence.WriteString("\n")
	}
	for _, it := range b.Untrusted {
		evidence.WriteString(it.Content)
		evidence.WriteString("\n")
	}
	hay := strings.ToLower(evidence.String())
	var gaps []string
	for _, n := range needs {
		if n = strings.TrimSpace(n); n == "" {
			continue
		}
		if !strings.Contains(hay, strings.ToLower(n)) {
			gaps = append(gaps, n)
		}
	}
	if len(gaps) == 0 {
		return SufficiencyReport{Outcome: Sufficient}
	}
	return SufficiencyReport{Outcome: NeedsRecall, Gaps: gaps}
}

// EnsureSufficient runs the bounded E2 ↔ E2.5 loop: assemble, check, and on
// gaps re-assemble with a GAP-FOCUSED query (the gaps join the recall query,
// steering keyword retrieval at exactly the missing evidence). Exhausting
// the cycle budget without sufficiency yields NeedsHuman with the surviving
// gaps — escalate, never silently proceed.
func (e *Engine) EnsureSufficient(ctx context.Context, taskID, intent string, needs []string) (Bundle, SufficiencyReport, error) {
	maxCycles := maxSufficiencyCycles()
	query := intent
	var bundle Bundle
	var rep SufficiencyReport
	for cycle := 1; cycle <= maxCycles; cycle++ {
		b, err := e.Assemble(ctx, taskID, query)
		if err != nil {
			return Bundle{}, SufficiencyReport{}, err
		}
		bundle = b
		rep = CheckSufficiency(bundle, needs)
		rep.Cycles = cycle
		if rep.Outcome == Sufficient {
			return bundle, rep, nil
		}
		// Next cycle recalls with the gaps IN the query.
		query = intent + " " + strings.Join(rep.Gaps, " ")
	}
	rep.Outcome = NeedsHuman
	return bundle, rep, nil
}

// String renders the report for phase lines / escalation detail.
func (r SufficiencyReport) String() string {
	if r.Outcome == Sufficient {
		return fmt.Sprintf("evidence sufficient (cycle %d)", r.Cycles)
	}
	return fmt.Sprintf("%s after %d cycle(s) — missing evidence: %s", r.Outcome, r.Cycles, strings.Join(r.Gaps, "; "))
}
