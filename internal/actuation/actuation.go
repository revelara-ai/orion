// Package actuation defines the mandatory dry-run contract for state-mutating
// tools (or-nwb, PRD SRE-Derived Refinements). Every tool that changes state —
// sandbox exec, worktree ops, integration merge, Polaris writes — MUST support
// dry_run=true, returning a predicted effect + blast radius WITHOUT mutating. The
// reversibility gate consumes the prediction; the deterministic actuation path is
// caller-agnostic (it does not care whether the caller is an agent or a human).
package actuation

// Prediction is what a mutating tool WOULD do under dry_run=true.
type Prediction struct {
	Tool        string `json:"tool"`
	Effect      string `json:"effect"`
	BlastRadius string `json:"blast_radius"`
	Mutated     bool   `json:"mutated"` // always false for a dry run
}

// ToolEffect catalogs one state-mutating tool: its effect, blast radius, and
// whether it supports the dry-run contract (it must).
type ToolEffect struct {
	Tool        string `json:"tool"`
	Effect      string `json:"effect"`
	BlastRadius string `json:"blast_radius"`
	DryRun      bool   `json:"dry_run"`
}

// Catalog returns every known state-mutating tool. The dry-run contract requires
// each entry to support dry_run; TestMutatingToolsSupportDryRun enforces it so a
// newly-added mutating tool cannot silently skip the contract.
func Catalog() []ToolEffect {
	return []ToolEffect{
		{Tool: "sandbox.exec", Effect: "executes generated code in an isolated sandbox", BlastRadius: "task sandbox (no host FS, egress denied)", DryRun: true},
		{Tool: "sandbox.write", Effect: "writes generated artifact files to disk", BlastRadius: "task worktree", DryRun: true},
		{Tool: "worktree.create", Effect: "creates a git worktree for a task", BlastRadius: "local worktrees dir + git metadata", DryRun: true},
		{Tool: "worktree.remove", Effect: "removes a git worktree", BlastRadius: "local worktree dir", DryRun: true},
		{Tool: "integration.merge", Effect: "merges a proven branch", BlastRadius: "local main branch", DryRun: true},
		{Tool: "polaris.write", Effect: "contributes sanitized knowledge to Polaris", BlastRadius: "remote Polaris (egress)", DryRun: true},
	}
}
