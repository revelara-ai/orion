package orchestrator

import (
	"context"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/formal"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
)

// Design-proof synthesis placement (or-56c.2): AFTER spec + STPA are ratified,
// BEFORE the plan persists — the design-time slot where a concurrent design
// earns a candidate formal model. Best-effort and fail-open: a synthesis miss
// (no synthesizer, no STPA yet, LLM error, invalid draft) never fails a plan —
// the artifact it produces is a DRAFT awaiting human ratification, and only
// ratification gives it proof authority.
func (c *Conductor) synthesizeDesignModel(ctx context.Context, projID string, es spec.ExecutableSpec) {
	if c.modelSynth == nil {
		return
	}
	model, ok, err := stpa.Load(ctx, c.store, projID)
	if err != nil || !ok {
		return // no ratified STPA yet — the synthesis input isn't complete
	}
	texts := make([]string, 0, len(es.Requirements))
	for _, r := range es.Requirements {
		texts = append(texts, r.Text)
	}
	in := formal.SynthesisInput{
		Intent:      es.Intent,
		DesignTexts: texts,
		Structure:   model.Structure,
		UCAs:        model.UCAs,
	}
	// Design time precedes any artifact scan, so the trigger runs at the
	// standard tier: the spec's SHAPE (concurrency vocabulary, multi-controller
	// structure) decides — never a silent always-on.
	dm, err := formal.SynthesizeDesignModel(ctx, c.store, projID, reliabilitytier.Standard, in, c.modelSynth)
	if err != nil {
		c.log.WarnContext(ctx, "design-model synthesis failed — plan proceeds without a draft", "err", err)
		return
	}
	if dm != nil && !dm.Ratified {
		c.log.InfoContext(ctx, "design-proof model drafted — awaiting human ratification",
			"hash", dm.Hash, "backend", dm.Backend, "trigger", dm.TriggerReason)
	}
}

// SetModelSynthesizer injects the design-model drafting adapter (or-56c.2).
// Safe to leave nil — synthesis is skipped without one.
func (c *Conductor) SetModelSynthesizer(s formal.Synthesizer) { c.modelSynth = s }
