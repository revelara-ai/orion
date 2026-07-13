package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
)

// SubIntent is one sub-spec of a ratified spec-of-specs split (or-045a.4): a
// full feature of the project without which the top-level spec is unfulfilled,
// intaken as its own child project with its own independently ratified spec.
type SubIntent struct {
	Name   string `json:"name"`
	Intent string `json:"intent"`
}

// RatifySplit records the developer-confirmed decomposition of the ACTIVE
// project into sub-spec child projects (or-045a.4). Guards mirror the other
// ratification acts: the project type must be resolved (children inherit it),
// the goals must be RATIFIED (the split derives from them), and no blocking
// open question may be pending in any phase. Each child is created QUEUED with
// its own draft spec, inherits the parent's ratified direction decisions
// (re-ratifiable — a child answer overrides) and STPA model; the first child
// takes the active slot while the parent waits queued until the roll-up
// delivers it.
func (c *Conductor) RatifySplit(ctx context.Context, subs []SubIntent) ([]contextstore.Project, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.store == nil {
		return nil, errNoStore
	}
	proj, sp, err := c.currentProjectSpec(ctx) // strict active: a split is a mutation
	if err != nil {
		return nil, fmt.Errorf("ratify_split: no active project: %w", err)
	}
	if proj.ProjectType == completeness.Unclassified {
		return nil, fmt.Errorf("cannot ratify the split: the project type is unresolved — children inherit it; confirm it with the developer (set_project_type) first")
	}
	if len(subs) < 2 {
		return nil, fmt.Errorf("cannot ratify the split: %d sub-spec(s) — a spec-of-specs needs at least 2 (one sub-spec is just the flat spec)", len(subs))
	}
	for i, s := range subs {
		if strings.TrimSpace(s.Name) == "" || strings.TrimSpace(s.Intent) == "" {
			return nil, fmt.Errorf("cannot ratify the split: sub-spec %d needs both a name and an intent", i+1)
		}
	}
	if _, status, _, gerr := c.Goals(ctx); gerr != nil || status != "ratified" {
		return nil, fmt.Errorf("cannot ratify the split: the sub-specs derive from RATIFIED goals — propose_goals → ratify_goals first")
	}
	if qs := c.blockingOpenQuestions(ctx, proj.ID, ""); len(qs) > 0 {
		return nil, fmt.Errorf("cannot ratify the split: %d blocking open question(s) — answer or explicitly assume each (resolve_open_question):%s", len(qs), renderOpenQuestions(qs))
	}

	// Read what the children inherit BEFORE mutating: the parent's latest
	// direction decisions and its ratified goal-altitude STPA model (if any).
	parentDecisions, err := c.store.DecisionsForSpec(ctx, sp.ID)
	if err != nil {
		return nil, fmt.Errorf("ratify_split: read parent decisions: %w", err)
	}
	model, hasModel, err := stpa.Load(ctx, c.store, proj.ID)
	if err != nil {
		return nil, fmt.Errorf("ratify_split: read parent hazard model: %w", err)
	}

	var children []contextstore.Project
	err = c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		if existing, e := tx.Projects().ChildrenOf(ctx, proj.ID); e != nil {
			return e
		} else if len(existing) > 0 {
			return fmt.Errorf("cannot ratify the split: the project is already split into %d sub-specs — amend the children individually", len(existing))
		}
		for _, s := range subs {
			pid, e := tx.Projects().CreateChild(ctx, proj.ID, strings.TrimSpace(s.Name), strings.TrimSpace(s.Intent), proj.ProjectType)
			if e != nil {
				return e
			}
			specID, e := tx.Specs().CreateDraft(ctx, pid)
			if e != nil {
				return e
			}
			for _, d := range parentDecisions {
				if !strings.HasPrefix(d.Key, "direction.") {
					continue
				}
				if _, e := tx.Decisions().Create(ctx, pid, specID, d.Key, d.Value, "inherited", d.SecurityRelevant); e != nil {
					return e
				}
			}
			child, e := tx.Projects().Get(ctx, pid)
			if e != nil {
				return e
			}
			children = append(children, child)
		}
		// Handoff: the parent leaves the single active slot (it waits queued
		// until the roll-up delivers it) and the first sub-spec starts.
		if e := tx.Projects().SetStatus(ctx, proj.ID, "queued"); e != nil {
			return e
		}
		if e := tx.Projects().SetStatus(ctx, children[0].ID, "active"); e != nil {
			return e
		}
		children[0].Status = "active"
		return nil
	})
	if err != nil {
		return nil, err
	}
	// The STPA model copy rides outside the tx (stpa.Save opens its own); a
	// failure here leaves the children on the domain-neutral skeleton fallback.
	if hasModel {
		for _, child := range children {
			if err := stpa.Save(ctx, c.store, child.ID, model); err != nil {
				return children, fmt.Errorf("split ratified but the hazard model copy failed (children fall back to the skeleton): %w", err)
			}
		}
	}
	c.mu.Lock()
	c.projectID = children[0].ID
	c.mu.Unlock()
	c.log.InfoContext(ctx, "spec-of-specs ratified", "parent", proj.ID, "children", len(children))
	return children, nil
}

// ProjectTree renders the spec-of-specs tree for the current project — from
// the parent's or any child's seat — with per-project statuses (or-045a.4
// DONE-WHEN e). A flat project renders as a single line.
func (c *Conductor) ProjectTree(ctx context.Context) (string, error) {
	if c.store == nil {
		return "", errNoStore
	}
	proj, _, err := c.currentOrDeliveredProjectSpec(ctx)
	if err != nil {
		return "", fmt.Errorf("project_tree: no current project: %w", err)
	}
	root := proj
	if proj.ParentProjectID != "" {
		if err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
			var e error
			root, e = tx.Projects().Get(ctx, proj.ParentProjectID)
			return e
		}); err != nil {
			return "", err
		}
	}
	var children []contextstore.Project
	if err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		children, e = tx.Projects().ChildrenOf(ctx, root.ID)
		return e
	}); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s] — %s\n", root.Name, root.Status, root.Intent)
	if len(children) == 0 {
		b.WriteString("  (flat project: no sub-specs)\n")
		return b.String(), nil
	}
	delivered := 0
	for i, ch := range children {
		marker := "├─"
		if i == len(children)-1 {
			marker = "└─"
		}
		fmt.Fprintf(&b, "  %s %s [%s] — %s\n", marker, ch.Name, ch.Status, ch.Intent)
		if ch.Status == "delivered" {
			delivered++
		}
	}
	fmt.Fprintf(&b, "roll-up: %d/%d sub-specs delivered — the parent delivers only when all have\n", delivered, len(children))
	return b.String(), nil
}
