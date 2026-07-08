// Package contextwindow governs Orion's conversation size against the model's
// context window. It is provider-agnostic and window-relative: every defensive
// layer engages at a FRACTION of the window, so an 8K local brain and a 1M
// Anthropic brain are bounded by the same policy at different absolute counts.
//
// Cheapest lever first: CLEAR bulky (re-fetchable) tool-result bodies, then
// COMPACT the dialogue into a summary, with a hard GUARD below the true ceiling
// that leaves headroom for the model's own output. The sensing primitives
// (EstimateTokens, TokenCounter, ErrContextOverflow) live in package llm.
package contextwindow

import "github.com/revelara-ai/orion/pkg/llm"

// Fractions of the window at which each layer engages. Guard sits below 1.0 to
// leave room for the response tokens the model still needs to generate.
const (
	ClearFraction   = 0.40
	CompactFraction = 0.70
	GuardFraction   = 0.85
)

// DefaultWindow is the assumed context window (tokens) for a provider that does
// not advertise one via llm.ContextWindow. Conservative: better to shrink a
// little early on an unknown big model than to overflow an unknown small one.
const DefaultWindow = 128_000

// Policy is the set of absolute token thresholds derived from a window.
type Policy struct {
	Window    int // the model's context window
	ClearAt   int // estimate ≥ this → clear old tool-result bodies
	CompactAt int // estimate ≥ this → summarize the dialogue
	GuardAt   int // estimate ≥ this → hard limit; force-shrink before sending
}

// For derives the threshold policy from a context window.
func For(window int) Policy {
	return Policy{
		Window:    window,
		ClearAt:   int(float64(window) * ClearFraction),
		CompactAt: int(float64(window) * CompactFraction),
		GuardAt:   int(float64(window) * GuardFraction),
	}
}

// WindowOf returns the provider's advertised context window, or fallback when it
// does not implement the optional llm.ContextWindow capability (local/offline
// brains). A provider reporting a non-positive window is treated as unknown.
func WindowOf(prov llm.Provider, fallback int) int {
	if cw, ok := prov.(llm.ContextWindow); ok {
		if w := cw.ContextWindow(); w > 0 {
			return w
		}
	}
	return fallback
}
