package skill

import (
	"os"
	"path/filepath"
	"strings"
)

// Scope is one discovery root plus the trust tier the loader assigns to every skill found
// there (trust is scope-assigned, never self-declared — see registry.go).
type Scope struct {
	Root  string
	Trust Trust
}

// DefaultScopes returns the conventional agentskills.io discovery scopes in PRECEDENCE ORDER —
// user scopes first, project scopes last — so a project skill overrides a same-named user
// skill (the standard project-over-user rule, since later loads win). It scans the
// cross-client ".agents/skills" convention plus the pragmatic ".claude/skills" and the
// Orion-native ".orion/skills" locations, so a skill installed by any compliant client is
// visible unchanged. projectDir is normally the working directory; "" omits project scopes.
//
// All scopes are generation-trust (reloadable). A curated, immutable built-in scope (proof
// trust, invariant 8) is loaded separately by the caller when one exists.
func DefaultScopes(projectDir string) []Scope {
	var scopes []Scope
	add := func(base string) []Scope {
		return []Scope{
			{filepath.Join(base, ".agents", "skills"), TrustGeneration},
			{filepath.Join(base, ".claude", "skills"), TrustGeneration},
			{filepath.Join(base, ".codex", "skills"), TrustGeneration}, // or-ykz.10: cross-harness (Codex)
			{filepath.Join(base, ".orion", "skills"), TrustGeneration},
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		scopes = append(scopes, add(home)...)
	}
	if projectDir != "" {
		scopes = append(scopes, add(projectDir)...)
	}
	// or-ykz.10: configured import paths (ORION_SKILL_DIRS, os.PathListSeparator-
	// joined) load as ingested generation-domain skills too.
	for _, p := range filepath.SplitList(os.Getenv("ORION_SKILL_DIRS")) {
		if p = filepath.Clean(strings.TrimSpace(p)); p != "" && p != "." {
			scopes = append(scopes, Scope{Root: p, Trust: TrustGeneration})
		}
	}
	return scopes
}

// LoadScopes loads every scope into the registry in order (later overrides earlier). A missing
// scope directory is a no-op, so the conventional set can be passed wholesale.
func (r *Registry) LoadScopes(scopes []Scope) error {
	for _, s := range scopes {
		if _, err := r.LoadDir(s.Root, s.Trust); err != nil {
			return err
		}
	}
	return nil
}
