package conductor

import (
	"github.com/revelara-ai/orion/internal/contextwindow"
	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/sandbox"
	"github.com/revelara-ai/orion/pkg/llm"
)

// moduleFitBudgetFraction: a module's estimated generation context must fit
// in this fraction of the provider window — the other half is headroom for
// the response, tool results, and the refinement turn (or-7et.3).
const moduleFitBudgetFraction = 0.5

// NewModuleFitEstimator sizes a proposed module the way the generator will
// actually pay for it (or-7et.3): the rendered generation prompt for the
// module's spec slice, plus the refinement re-injection (the module's own
// source fed back on attempt 2, approximated by the slice-proportional
// prompt cost again).
func NewModuleFitEstimator(provider llm.Provider, es spec.ExecutableSpec) decomposer.FitEstimator {
	window := contextwindow.WindowOf(provider, contextwindow.DefaultWindow)
	budget := int(float64(window) * moduleFitBudgetFraction)
	return func(m decomposer.ProposedModule) decomposer.FitEstimate {
		covered := map[string]bool{}
		for _, c := range m.Covers {
			covered[c] = true
		}
		gs := sandbox.GenSpec{Module: "orion-generated/" + m.Key}
		for _, c := range es.ResponseContract.Cases {
			if covered[c.ID] {
				gs.Cases = append(gs.Cases, c)
			}
		}
		prompt := GenerationPrompt(gs, "")
		tokens := llm.EstimateTokens(llm.ChatRequest{System: prompt}) +
			len(m.FileScope)/4 + // brownfield scope source rides the context
			len(m.ProofObligation)/4
		// Refinement re-injects the module's own prior source (nativegen):
		// approximate the module source as the same order as its prompt slice.
		tokens *= 2
		return decomposer.FitEstimate{Tokens: tokens, Budget: budget}
	}
}

// genWindow is the active generation provider's context window, set at the
// tool seam (SetGenerationWindow); 0 falls back to the conservative default.
var genWindow int

// SetGenerationWindow records the active provider's window for token-budgeted
// context assembly (or-7et.3).
func SetGenerationWindow(tokens int) { genWindow = tokens }

func generationWindow() int {
	if genWindow > 0 {
		return genWindow
	}
	return contextwindow.DefaultWindow
}
