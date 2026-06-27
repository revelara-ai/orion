package skill

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AgentRegistry is an in-memory set of agent definitions keyed by name, plus any discovered
// freeform AGENTS.md docs and load diagnostics. It parallels Registry (skills), but agents are
// single .md files in an agents/ directory (not directories with a manifest).
type AgentRegistry struct {
	agents   map[string]Agent
	docs     []AgentsDoc
	warnings []string
}

// NewAgentRegistry returns an empty agent registry.
func NewAgentRegistry() *AgentRegistry { return &AgentRegistry{agents: map[string]Agent{}} }

// LoadDir discovers subagent .md files directly under root (non-recursive — agent files live
// flat in an agents/ dir) at the scope-assigned trust. A symlinked file is skipped (no escape).
// A later load overrides an earlier one within the same trust; a proof-trust agent cannot be
// shadowed by a generation one (invariant 8). A missing root is a no-op.
func (r *AgentRegistry) LoadDir(root string, trust Trust) (int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, nil
	}
	loaded := 0
	for _, e := range entries {
		if e.IsDir() || e.Type()&fs.ModeSymlink != 0 || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(root, e.Name())
		content, rerr := readCapped(path, maxSkillBytes)
		if rerr != nil {
			r.warnings = append(r.warnings, fmt.Sprintf("%s: %v", path, rerr))
			continue
		}
		a, ws, perr := ParseAgent(content)
		r.warnings = append(r.warnings, ws...)
		if perr != nil {
			r.warnings = append(r.warnings, fmt.Sprintf("%s: %v", path, perr))
			continue
		}
		if abs, aerr := filepath.Abs(path); aerr == nil {
			path = abs
		}
		a.Path = path
		a.Trust = trust
		if existing, exists := r.agents[a.Name]; exists {
			if existing.Trust == TrustProof && a.Trust != TrustProof {
				r.warnings = append(r.warnings, fmt.Sprintf("agent %q at %s ignored — a proof-trust agent of that name is immutable", a.Name, path))
				continue
			}
			r.warnings = append(r.warnings, fmt.Sprintf("agent %q at %s shadows earlier load at %s", a.Name, path, existing.Path))
		}
		r.agents[a.Name] = a
		loaded++
	}
	return loaded, nil
}

// LoadScopes loads every scope in order (later overrides earlier within the same trust).
func (r *AgentRegistry) LoadScopes(scopes []Scope) error {
	for _, s := range scopes {
		if _, err := r.LoadDir(s.Root, s.Trust); err != nil {
			return err
		}
	}
	return nil
}

// Get returns the agent registered under name.
func (r *AgentRegistry) Get(name string) (Agent, bool) { a, ok := r.agents[name]; return a, ok }

// List returns all agents, sorted by name.
func (r *AgentRegistry) List() []Agent {
	out := make([]Agent, 0, len(r.agents))
	for _, a := range r.agents {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Docs returns the discovered freeform AGENTS.md docs.
func (r *AgentRegistry) Docs() []AgentsDoc { return r.docs }

// Warnings returns the non-fatal load diagnostics.
func (r *AgentRegistry) Warnings() []string { return r.warnings }

// Catalog renders a one-line name+description catalog of the agents.
func (r *AgentRegistry) Catalog() string {
	agents := r.List()
	if len(agents) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# AVAILABLE AGENTS\n")
	for _, a := range agents {
		fmt.Fprintf(&b, "- %s: %s\n", a.Name, oneLine(a.Description))
	}
	return b.String()
}

// DiscoverAgentsDocs scans the given dirs for an AGENTS.md (the cross-tool freeform
// agent-instructions convention) and adds each found to the registry's Docs. Best-effort.
func (r *AgentRegistry) DiscoverAgentsDocs(dirs ...string) {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, "AGENTS.md")
		content, err := readCapped(path, maxSkillBytes)
		if err != nil {
			continue
		}
		if abs, aerr := filepath.Abs(path); aerr == nil {
			path = abs
		}
		r.docs = append(r.docs, AgentsDoc{Content: string(content), Path: path})
	}
}

// DefaultAgentScopes returns the conventional subagent discovery dirs in precedence order
// (user before project): ~/.agents/agents, ~/.claude/agents + project equivalents. All
// generation-trust (reloadable); a curated built-in scope (proof) is added by the caller.
func DefaultAgentScopes(projectDir string) []Scope {
	var scopes []Scope
	add := func(base string) []Scope {
		return []Scope{
			{filepath.Join(base, ".agents", "agents"), TrustGeneration},
			{filepath.Join(base, ".claude", "agents"), TrustGeneration},
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		scopes = append(scopes, add(home)...)
	}
	if projectDir != "" {
		scopes = append(scopes, add(projectDir)...)
	}
	return scopes
}
