package decomposer

import "fmt"

// ShadowOutcome is one shadow run's deterministic comparison outcome (the
// decomposer-side projection of a persisted shadow record).
type ShadowOutcome struct {
	SupersetOK       bool // proposer coverage ⊇ oracle coverage
	FloorOK          bool // every reliability-floor dimension covered
	CoverageGateOK   bool // every requirement mapped to ≥1 module
	ProposerClusters int
	OracleClusters   int
}

// CutoverWindow is the default measured window the cutover criterion requires
// (or-809: ">=50 runs" from the ratified design).
const CutoverWindow = 50

// CutoverReady evaluates the deterministic shadow→live cutover criterion over
// the newest `window` outcomes (order: newest first): EVERY run must hold
// coverage-superset, floor, and coverage-gate, AND cluster-count
// non-regression (the judge-panel hazard: a FileScope collision collapses the
// DAG to one cluster — coverage stays green while parallelism dies). It
// reports readiness plus a human-readable reason; the FLIP itself stays a
// human decision (set ORION_MODULE_PROPOSER=live).
func CutoverReady(outcomes []ShadowOutcome, window int) (bool, string) {
	if window <= 0 {
		window = CutoverWindow
	}
	if len(outcomes) < window {
		return false, fmt.Sprintf("measured window incomplete: %d/%d shadow runs", len(outcomes), window)
	}
	for i, o := range outcomes[:window] {
		switch {
		case !o.SupersetOK:
			return false, fmt.Sprintf("run %d: proposer coverage is not a superset of the oracle's", i)
		case !o.FloorOK:
			return false, fmt.Sprintf("run %d: reliability floor dimension uncovered", i)
		case !o.CoverageGateOK:
			return false, fmt.Sprintf("run %d: coverage gate failed", i)
		case o.ProposerClusters < o.OracleClusters:
			return false, fmt.Sprintf("run %d: cluster-count regression (%d < oracle %d) — parallelism would collapse", i, o.ProposerClusters, o.OracleClusters)
		}
	}
	return true, fmt.Sprintf("all %d runs green: superset+floor+coverage hold, no cluster regression", window)
}
