package orchestrator

import (
	"context"
	"fmt"

	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
)

// RatifyGoalHazards records the developer-ratified GOAL-ALTITUDE hazard
// analysis (or-045a.3): the losses the project must not cause and the loss
// scenarios that would cause them, drafted from the RATIFIED goals document
// (the source artifact — no goals, no hazard model). The control side rides
// the domain-neutral skeleton's closed loop until build time (no code exists
// yet to model); the stpa.Questionnaire enforces its own gate order, and the
// completed model persists via stpa.Save exactly where the build's hazard
// gate Loads it — the SkeletonModel fallback becomes the exception.
func (c *Conductor) RatifyGoalHazards(ctx context.Context, losses []stpa.Loss, scenarios []stpa.LossScenario) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.store == nil {
		return errNoStore
	}
	proj, _, err := c.currentProjectSpec(ctx)
	if err != nil {
		return err
	}
	// The losses are extrapolated FROM the goals — require the source artifact.
	if c.GoalsSummary(ctx) == "" {
		return fmt.Errorf("ratify_losses: no ratified goals document — losses are extrapolated from the goals; run propose_goals → ratify_goals first")
	}
	// Coherence: every scenario must reference a ratified loss.
	lossIDs := map[string]bool{}
	for _, l := range losses {
		lossIDs[l.ID] = true
	}
	for _, s := range scenarios {
		if !lossIDs[s.Loss] {
			return fmt.Errorf("ratify_losses: scenario %q references unknown loss %q", s.ID, s.Loss)
		}
	}
	if len(scenarios) == 0 {
		return fmt.Errorf("ratify_losses: at least one loss scenario is required (what would CAUSE a loss)")
	}

	// Drive the questionnaire through its own gates: goal losses + scenarios,
	// with the skeleton's closed control loop standing in for the not-yet-built
	// system (its generic UCA re-anchored onto the ratified losses).
	skeleton := stpa.SkeletonModel()
	allIDs := make([]string, 0, len(losses))
	for _, l := range losses {
		allIDs = append(allIDs, l.ID)
	}
	ucas := skeleton.UCAs
	for i := range ucas {
		ucas[i].LossRefs = allIDs
	}
	q := stpa.New()
	if err := q.RatifyLosses(losses); err != nil {
		return err
	}
	if err := q.RatifyControlStructure(skeleton.Structure); err != nil {
		return err
	}
	if err := q.RatifyUCAs(ucas); err != nil {
		return err
	}
	if err := q.RatifyLossScenarios(scenarios); err != nil {
		return err
	}
	model, err := q.Model()
	if err != nil {
		return err
	}
	if err := stpa.Save(ctx, c.store, proj.ID, model); err != nil {
		return err
	}
	c.log.InfoContext(ctx, "goal hazard model ratified", "losses", len(losses), "scenarios", len(scenarios))
	return nil
}

// goalHazardsRatified reports whether a ratified hazard model exists for the
// active project (the ApproveSpec gate for large-scale intakes).
func (c *Conductor) goalHazardsRatified(ctx context.Context, projectID string) bool {
	_, found, err := stpa.Load(ctx, c.store, projectID)
	return err == nil && found
}
