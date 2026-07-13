package orchestrator

import (
	"context"
	"fmt"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// AmendView reports what an amendment draft was seeded with.
type AmendView struct {
	SpecID              string `json:"spec_id"`
	Version             int    `json:"version"`
	ParentSpecID        string `json:"parent_spec_id"`
	RequirementsCarried int    `json:"requirements_carried"`
	DecisionsCarried    int    `json:"decisions_carried"`
}

// AmendSpec starts a refactor on a repo that already has a RATIFIED spec
// (or-tcs.5.1): it opens a new draft version seeded from the prior spec —
// requirements copied, answered decisions carried over, lineage recorded via
// parent_spec_id — so the developer edits (add/remove_requirement,
// record_answer) instead of re-eliciting from scratch. The ratified parent is
// never mutated; re-ratifying the draft mints a NEW hash, which the stale-plan
// guard (or-7et.2) then holds every old plan read against.
func (c *Conductor) AmendSpec(ctx context.Context) (AmendView, error) {
	if c.store == nil {
		return AmendView{}, errNoStore
	}
	proj, sp, err := c.currentProjectSpec(ctx)
	if err != nil {
		return AmendView{}, fmt.Errorf("no current spec to amend: %w", err)
	}
	if sp.Status != "accepted" {
		return AmendView{}, fmt.Errorf("the current spec (v%d) is still a draft — keep editing it (add_requirement / record_answer / approve_spec); amendment starts from a RATIFIED spec", sp.Version)
	}
	av := AmendView{ParentSpecID: sp.ID, Version: sp.Version + 1}
	av.RequirementsCarried = len(loadRequirements(sp.Requirements))
	err = c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		id, e := tx.Specs().CreateAmendmentDraft(ctx, sp)
		if e != nil {
			return e
		}
		av.SpecID = id
		// Carry the ANSWERED decisions: the draft starts with the parent's
		// choices and the developer revises the ones the refactor changes.
		ds, e := tx.Decisions().ListForSpec(ctx, sp.ID)
		if e != nil {
			return e
		}
		for _, d := range ds {
			if _, e := tx.Decisions().Create(ctx, proj.ID, id, d.Key, d.Value, d.ValueKind, d.SecurityRelevant); e != nil {
				return e
			}
			av.DecisionsCarried++
		}
		return nil
	})
	if err != nil {
		return AmendView{}, err
	}
	c.log.InfoContext(ctx, "spec amendment draft opened",
		"spec_id", av.SpecID, "version", av.Version, "parent", sp.ID,
		"requirements", av.RequirementsCarried, "decisions", av.DecisionsCarried)
	return av, nil
}
