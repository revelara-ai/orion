// Package delivery evaluates the deployment bar and produces a proven,
// human-mergeable change with a schematized operating envelope (or-fwl, PRD Trace
// 5 / Phase F). V2.0 never auto-deploys: when the bar is met the change is marked
// human-mergeable; when it is not, delivery routes to escalation — never a silent
// ship.
//
// Manifesto: proof is the right to ship; autonomy is earned (human-merge in V2.0).
package delivery

import (
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
)

// Decision is the deployment-bar outcome.
type Decision string

const (
	Deliver  Decision = "deliver"
	Escalate Decision = "escalate"
)

// OperatingEnvelope is the schematized "what was proven, under what conditions"
// record shown in the Delivery pane and attached to the deliverable (Stories 26–27).
type OperatingEnvelope struct {
	ProvenLoad                string   `json:"proven_load"`
	FaultClassesControlled    []string `json:"fault_classes_controlled"`
	Assumptions               []string `json:"assumptions"`
	Tier                      string   `json:"tier"`
	ReducedReliabilityContext bool     `json:"reduced_reliability_context,omitempty"` // revelara.ai context was unreachable → cache/empty (or-xe7.4)
}

// Result is the deployment-bar evaluation.
type Result struct {
	// AutonomyPermitted (or-v9f.30): the earned-autonomy ladder cleared —
	// Deliver decision + tier policy earned + red button clear. Consumers may
	// act post-proof without a per-change prompt; the explicit opt-in/out
	// still overrides.
	AutonomyPermitted bool
	Decision          Decision           `json:"decision"`
	HumanMergeable    bool               `json:"human_mergeable"`
	Envelope          *OperatingEnvelope `json:"operating_envelope"`
	Reason            string             `json:"reason"`
}

// EvaluateBar decides delivery vs escalation against the tier policy. The bar is
// met only when the proof verdict is Accept AND (for tiers that require it) all
// three modes converged. A met bar yields a human-mergeable delivery with the
// operating envelope; otherwise it escalates with a named reason.
func EvaluateBar(verdict truthalign.Verdict, presentModes []string, policy reliabilitytier.Policy, env OperatingEnvelope, securityOK bool, unverifiedOps []string) Result {
	if !securityOK {
		return Result{Decision: Escalate, Reason: "security gate failed: hardcoded secret in the artifact"}
	}
	if verdict != truthalign.Accept {
		return Result{Decision: Escalate, Reason: fmt.Sprintf("proof verdict is %s, not Accept", verdict)}
	}
	if policy.RequireAllModes && !hasAllModes(presentModes) {
		return Result{Decision: Escalate, Reason: fmt.Sprintf("tier %s requires behavioral+empirical+hazard; got %v", policy.Tier, presentModes)}
	}
	// or-v9f.13: a critical-tier delivery must document what was proven and what
	// faults are controlled — an empty envelope at the highest tier is exactly the
	// unstated-scaling-assumptions failure the manifesto names.
	if policy.RequireEnvelope && (env.ProvenLoad == "" || len(env.FaultClassesControlled) == 0) {
		return Result{Decision: Escalate, Reason: fmt.Sprintf("tier %s requires a complete operating envelope (proven load + controlled fault classes); got load=%q faults=%d", policy.Tier, env.ProvenLoad, len(env.FaultClassesControlled))}
	}
	// or-v9f.12: at the highest tier, a runbook claim the artifact cannot honor
	// is a delivery blocker — the 3 a.m. operator depends on those instructions.
	if policy.RequireEnvelope && len(unverifiedOps) > 0 {
		return Result{Decision: Escalate, Reason: fmt.Sprintf("tier %s requires verified operability; runbook claims lack artifact evidence: %s", policy.Tier, strings.Join(unverifiedOps, ", "))}
	}
	env.Tier = string(policy.Tier)
	return Result{Decision: Deliver, HumanMergeable: true, Envelope: &env, Reason: "bar met; human-mergeable (V2.0 no auto-deploy)"}
}

// ApplySupplyChain (or-ykz.16): a bar-cleared delivery is still blocked by
// known-vulnerable dependencies — OSV findings escalate a Deliver decision.
// Only POSITIVE findings block; an already-escalated result keeps its first
// (more fundamental) reason, and a skipped/offline audit changes nothing here
// (the caller surfaces the skip visibly instead).
func ApplySupplyChain(res Result, findingSummary string, findings int) Result {
	if res.Decision != Deliver || findings == 0 {
		return res
	}
	return Result{Decision: Escalate, Reason: "supply-chain gate failed: " + findingSummary}
}

func hasAllModes(modes []string) bool {
	present := map[string]bool{}
	for _, m := range modes {
		present[m] = true
	}
	for _, r := range truthalign.RequiredModes {
		if !present[r] {
			return false
		}
	}
	return true
}
