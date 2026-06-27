package conductor

import (
	"path/filepath"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/promptguard"
	"github.com/revelara-ai/orion/internal/skill"
)

// skillCatalogForGen builds the AVAILABLE-SKILLS catalog injected into the generation prompt:
// the user-scope skills (agentskills.io conventional dirs) plus the self-evolved skills under
// <dataDir>/skills (store.Dir()/skills, where `orion evolve` writes). It carries each skill's
// path so a file-read-capable generator can activate it.
//
// Skill descriptions can come from untrusted project skills, so the catalog is run through
// promptguard before it reaches the generator (defense-in-depth, reusing or-mkb). Returns "" if
// no skills are discoverable. No cwd dependency — discovery uses the user scopes + the data dir
// the store lives in.
func skillCatalogForGen(store *contextstore.Store) string {
	if store == nil {
		return ""
	}
	r := skill.New()
	scopes := skill.DefaultScopes("") // user scopes only (~/.agents, ~/.claude, ~/.orion)
	scopes = append(scopes, skill.Scope{Root: filepath.Join(store.Dir(), "skills"), Trust: skill.TrustGeneration})
	if err := r.LoadScopes(scopes); err != nil {
		return ""
	}
	cat := r.CatalogForGeneration()
	if cat == "" {
		return ""
	}
	safe, _ := promptguard.Neutralize(cat, promptguard.ScopeAll)
	return safe
}
