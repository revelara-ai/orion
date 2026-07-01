package conductor

import (
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// epicOutcome separates two facts the delivery tail needs (or-v9f.5): whether
// the EPIC fully proved (aggregate — honest: any failed task rejects it), and
// whether a proven, dependency-complete, wired SUBSET exists that can ship
// anyway (partial). barVerdict is the verdict of the artifact actually being
// delivered — the full tree or the accepted subset — and is what the deployment
// bar and the PR speak for.
type epicOutcome struct {
	aggregate  truthalign.Verdict
	partial    bool
	barVerdict truthalign.Verdict
}

// evaluateEpicOutcome computes the outcome from the per-task results, whether
// the accepted subset assembled cleanly (integrateEpic), and whether the
// assembled tree passed the structural wireup gate. An unwired or unassembled
// subset is NOT deliverable — partial delivery never lowers the bar, it only
// stops one failed task from suppressing proven siblings.
func evaluateEpicOutcome(results []taskResult, integrated, wired bool) epicOutcome {
	acceptedCount, failedCount := 0, 0
	for _, r := range results {
		switch {
		case r.Blocked:
		case r.Verdict == "Accept":
			acceptedCount++
		default:
			failedCount++
		}
	}
	out := epicOutcome{aggregate: truthalign.Accept}
	if failedCount > 0 || acceptedCount == 0 || !integrated || !wired {
		out.aggregate = truthalign.Reject
	}
	out.partial = integrated && wired && acceptedCount > 0 && failedCount > 0
	out.barVerdict = out.aggregate
	if out.partial {
		out.barVerdict = truthalign.Accept
	}
	return out
}

// escalatedRemainder renders what is NOT in a partial delivery — the failing
// tasks with their causal analyses plus the dependents they blocked — for the
// PR body and the phase report. Empty when nothing was left behind.
func escalatedRemainder(results []taskResult) string {
	var b strings.Builder
	for _, r := range results {
		switch {
		case r.Blocked:
			fmt.Fprintf(&b, "- %s: BLOCKED (a dependency did not prove)\n", r.TaskID)
		case r.Verdict != "Accept":
			reason := strings.TrimSpace(r.FailureAnalysis)
			if reason == "" {
				reason = "proof verdict " + r.Verdict
			}
			fmt.Fprintf(&b, "- %s: %s\n", r.TaskID, reason)
		}
	}
	return b.String()
}
