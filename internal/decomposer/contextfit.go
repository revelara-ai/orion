package decomposer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// Context-fit gate (or-7et.3): V3's load-bearing invariant — each module
// "fits in current context" — measured at plan time instead of asserted. A
// module whose ESTIMATED generation context exceeds its budget is rejected
// with a named error and routed back to the proposer ONCE with an explicit
// split instruction; a still-oversized re-proposal escalates. Never build a
// module whose first request already exceeds the window.

// ErrModuleOversized names the context-fit rejection.
var ErrModuleOversized = errors.New("decomposer: module exceeds the generation context budget")

// FitEstimate is one module's plan-time sizing.
type FitEstimate struct {
	Tokens int // estimated generation context (prompt + slice + scope + refinement re-injection)
	Budget int // the budget it must fit (a fraction of the provider window)
}

// FitEstimator sizes a proposed module. nil disables the gate.
type FitEstimator func(m ProposedModule) FitEstimate

// ContextFitGate rejects any module whose estimate exceeds its budget,
// naming every offender with its numbers (the split instruction's substance).
func ContextFitGate(mods []ProposedModule, fit FitEstimator) error {
	if fit == nil {
		return nil
	}
	var over []string
	for _, m := range mods {
		if m.Key == "acceptance" {
			continue // the deterministic bookend is Orion's, not a generated module
		}
		e := fit(m)
		if e.Budget > 0 && e.Tokens > e.Budget {
			over = append(over, fmt.Sprintf("%s (~%d tokens > budget %d)", m.Key, e.Tokens, e.Budget))
		}
	}
	if len(over) > 0 {
		return fmt.Errorf("%w: %s — split each into smaller vertical slices", ErrModuleOversized, strings.Join(over, "; "))
	}
	return nil
}

// splitHintKey carries the one-retry split instruction to the proposer.
type splitHintKey struct{}

// WithSplitHint attaches the split instruction the (LLM) proposer must obey
// on the context-fit retry.
func WithSplitHint(ctx context.Context, hint string) context.Context {
	return context.WithValue(ctx, splitHintKey{}, hint)
}

// SplitHintFrom returns the pending split instruction, if any.
func SplitHintFrom(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(splitHintKey{}).(string)
	return v, ok && v != ""
}

// ProposeFit is Propose with the context-fit gate: propose → gate → on an
// oversized module, ONE re-proposal with an explicit split instruction →
// gate again → still oversized escalates (the caller never builds it).
func ProposeFit(ctx context.Context, es spec.ExecutableSpec, projectType string, floor []completeness.Dimension, mp ModuleProposer, fit FitEstimator) (Epic, error) {
	mods, err := mp(ctx, es, projectType, floor)
	if err != nil {
		return Epic{}, fmt.Errorf("module proposer: %w", err)
	}
	if len(mods) == 0 {
		return Epic{}, fmt.Errorf("module proposer returned no modules")
	}
	if gerr := ContextFitGate(mods, fit); gerr != nil {
		// One split-routed retry: the proposer sees exactly which modules are
		// oversized and by how much.
		mods, err = mp(WithSplitHint(ctx, gerr.Error()), es, projectType, floor)
		if err != nil {
			return Epic{}, fmt.Errorf("module proposer (split retry): %w", err)
		}
		if gerr := ContextFitGate(mods, fit); gerr != nil {
			return Epic{}, fmt.Errorf("still oversized after a split retry — escalating, never building an over-window module: %w", gerr)
		}
	}
	return bookendEpic(es, floor, mods), nil
}
