package conductor

import (
	"context"
	"os"

	"github.com/revelara-ai/orion/internal/brownfield"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

// Codebase grounding at submit time (or-tcs.5): in a brownfield repo the
// grill must cite CODE-DERIVED facts, not invented structure — and that must
// not depend on the model remembering to call read_codebase. submit_intent's
// result auto-attaches a bounded repo digest, and the same facts are
// RECORDED on the project (polaris_context kind code_grounding) as the
// spec's citation trail. Greenfield yields "" — nothing to ground in.
const codeGroundingKind = "code_grounding"

const groundingMaxChars = 4000

// greenfieldIntake reports whether the session's active project is a NEW
// standalone build rather than a change to the cwd repo (or-hn15.1). When true,
// the cwd's codebase is an UNRELATED host repo — not the build target — so it
// must not ground the grill, the spec, or read_codebase (the dogfood ran a
// greenfield game intent from inside the Orion repo and inherited Orion's Go
// map). The change flow attributes to the reserved brownfield project; every
// other active project is a greenfield build. No active project yet ⇒ false
// (the intent is unknown — the caller keeps its default behavior).
func greenfieldIntake(ctx context.Context, c *orchestrator.Conductor) bool {
	st := c.Store()
	if st == nil {
		return false
	}
	proj, _, err := st.CurrentProjectSpec(ctx)
	if err != nil {
		return false
	}
	return proj.Name != contextstore.BrownfieldProjectName && proj.ProjectType != "brownfield"
}

func codebaseGrounding(ctx context.Context, c *orchestrator.Conductor) string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	m := brownfield.ScanRepoMap(dir)
	if m.Profile.Mode == brownfield.Greenfield {
		return ""
	}
	digest := m.Digest()
	if len(digest) > groundingMaxChars {
		digest = digest[:groundingMaxChars] + "\n… (truncated — read_codebase for the full map)"
	}
	// Audit trail: the facts the grill was grounded in, on the project record.
	if st := c.Store(); st != nil && digest != "" {
		projID := ""
		if proj, _, perr := st.CurrentProjectSpec(ctx); perr == nil {
			projID = proj.ID
		}
		_ = st.WithTx(ctx, func(tx *contextstore.Tx) error {
			if projID == "" {
				// Routed pre-submit (or-3p5.10): the change flow's audit trail
				// lives on the reserved brownfield project.
				id, e := tx.Projects().GetOrCreateReserved(ctx, contextstore.BrownfieldProjectName, "brownfield")
				if e != nil {
					return e
				}
				projID = id
			}
			return tx.PolarisContext().Upsert(ctx, projID, codeGroundingKind, digest, 0)
		})
	}
	return digest
}
