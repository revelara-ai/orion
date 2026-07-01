package conductor

import (
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof"
)

// driftReport re-evaluates the INTEGRATED build against the spec artifact (or-tcs.10): the spec is
// the drift reference the development loop checks back against. Two dimensions it can judge today:
//   - COVERAGE (spec → build): every required spec obligation (a behavioral case id) must be
//     executed AND passing in the assembled proof — nothing in the spec went unbuilt.
//   - WIREUP (build → reachable): no orphan package — nothing built went unwired (from or-tcs.3).
//
// It returns a human-readable re-evaluation + whether DRIFT was found, and reports ONLY the two
// dimensions it actually re-evaluates against the build (coverage + wireup) — it makes no claim it
// did not check. The coverage gate (EnforceObligations) and the wireup gate already REJECT on drift;
// this SURFACES the spec↔build alignment so the developer sees it, and is the structured hook the
// third dimension — scope creep (built ↔ spec, needing code↔requirement traceability) — extends once
// builds produce distinct modules. `report` MUST be the proof of the ASSEMBLED tree (so coverage is
// judged against the delivered whole, not one cluster's slice).
func driftReport(es spec.ExecutableSpec, report proof.Report, orphans []string) (string, bool) {
	// Distinct required obligations: RequiredCaseIDs can repeat a content-addressed id when two
	// cases collapse to the same (request, expect) — e.g. a requirement restating the bare happy
	// path. Count each distinct obligation ONCE so the coverage fraction this report cites is honest.
	seen := make(map[string]bool)
	var required []string
	for _, id := range es.ResponseContract.RequiredCaseIDs() {
		if !seen[id] {
			seen[id] = true
			required = append(required, id)
		}
	}
	var uncovered []string
	for _, id := range required {
		if o, ok := report.ObligationResults[id]; !ok || !o.Executed || !o.Passed {
			uncovered = append(uncovered, id)
		}
	}
	drift := len(uncovered) > 0 || len(orphans) > 0

	var b strings.Builder
	b.WriteString("spec↔build drift check — ")
	if drift {
		b.WriteString("DRIFT")
	} else {
		b.WriteString("aligned")
	}
	fmt.Fprintf(&b, ": coverage %d/%d spec obligations proven", len(required)-len(uncovered), len(required))
	if len(uncovered) > 0 {
		fmt.Fprintf(&b, " (unbuilt: %s)", strings.Join(uncovered, ", "))
	}
	if len(orphans) > 0 {
		fmt.Fprintf(&b, "; wireup: orphan package(s) %s", strings.Join(orphans, ", "))
	} else {
		b.WriteString("; wireup clean")
	}
	return b.String(), drift
}
