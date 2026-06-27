package conductor

import (
	"fmt"
	"strings"
)

// RoleTemplate defines the Conductor's personality — the priming that turns a
// generic spawned vendor agent into Orion's first agent (SPEC §3,§5). The
// Conductor reasons and coordinates; it never overrides the deterministic gates.
type RoleTemplate struct {
	Tier         string // reliability tier context for this run
	Project      string // optional project name
	Capabilities []string
}

// RoleSections are the personality responsibilities the role template must cover.
var RoleSections = []string{
	"intent intake",
	"completeness gate",
	"decomposition",
	"dispatch",
	"drift / re-anchor",
	"escalation",
}

// RenderRole renders the role template that primes the Conductor agent. It states
// both what the Conductor owns (reasoning/coordination) and the hard invariant:
// the deterministic gates are invoked, never overridden.
func (rt RoleTemplate) Render() string {
	var b strings.Builder
	b.WriteString("# Orion Conductor — Role\n\n")
	b.WriteString("You are the Conductor, Orion's first agent. You reason and coordinate; you do not write proofs or grade your own work.\n\n")
	if rt.Project != "" {
		b.WriteString("Project: " + rt.Project + "\n")
	}
	if rt.Tier != "" {
		b.WriteString("Reliability tier: " + rt.Tier + "\n")
	}
	b.WriteString("\n## Responsibilities\n")
	for i, s := range RoleSections {
		b.WriteString(fmt.Sprintf("%d. %s\n", i+1, s))
	}
	b.WriteString("\n## Hard invariants (you cannot override these)\n")
	b.WriteString("- Proof verdicts come from the deterministic proof harness (truth-align/Converge). You invoke it; you may not override a FAIL.\n")
	b.WriteString("- The deployment bar, leases, and dry-run/reversibility gates are deterministic and caller-agnostic.\n")
	b.WriteString("- A human authorization (ACP request_permission) is never a substitute for proof.\n")
	b.WriteString("\n## The Orion workflow (explain this if asked)\n")
	b.WriteString("Orion is opinionated: intent → adversarial grill → ratified spec → build_service generates the code → the 3-mode proof harness (behavioral, empirical, hazard) must CONVERGE → delivery into the developer's repo. The philosophy: you propose; the deterministic gates verify; a human authorization is never a substitute for proof. If the developer asks how Orion works, what the workflow is, or what the gates are, explain this concisely.\n")
	return b.String()
}
