package llm

import "strings"

// Pricing (or-v9f.28): USD per MILLION tokens, all four classes. Embedded
// defaults for known hosted models; unknown/local models are UNPRICED —
// callers must mark them so (never a silent $0).
type Pricing struct {
	InPerM, OutPerM, CacheReadPerM, CacheWritePerM float64
}

// pricingTable maps provider/model-prefix → pricing. Longest prefix wins.
var pricingTable = map[string]Pricing{
	"anthropic/claude-opus":   {15, 75, 1.5, 18.75},
	"anthropic/claude-sonnet": {3, 15, 0.3, 3.75},
	"anthropic/claude-haiku":  {0.8, 4, 0.08, 1},
	"gemini/gemini-2.5-pro":   {1.25, 10, 0.31, 0},
	"gemini/gemini-2.5-flash": {0.30, 2.5, 0.075, 0},
	"openai/gpt-4o-mini":      {0.15, 0.6, 0.075, 0},
	"openai/gpt-4o":           {2.5, 10, 1.25, 0},
}

// PriceFor resolves pricing for a provider+model (prefix match, longest key
// wins). ok=false means UNPRICED (local/unknown) — token accounting only.
func PriceFor(provider, model string) (Pricing, bool) {
	key := strings.ToLower(strings.TrimSpace(provider)) + "/" + strings.ToLower(strings.TrimSpace(model))
	var best string
	for k := range pricingTable {
		if strings.HasPrefix(key, k) && len(k) > len(best) {
			best = k
		}
	}
	if best == "" {
		return Pricing{}, false
	}
	return pricingTable[best], true
}

// CostOf prices a usage record across all four token classes.
func CostOf(u Usage, p Pricing) float64 {
	const m = 1_000_000.0
	return float64(u.InputTokens)/m*p.InPerM +
		float64(u.OutputTokens)/m*p.OutPerM +
		float64(u.CacheReadInputTokens)/m*p.CacheReadPerM +
		float64(u.CacheCreationInputTokens)/m*p.CacheWritePerM
}
