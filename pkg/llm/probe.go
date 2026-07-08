package llm

import (
	"context"
	"encoding/json"
)

const probeToolName = "echo"

// Probe verifies the provider's active model can drive native tool calling
// with ONE minimal round-trip: it offers a single echo tool and instructs the
// model to call it. True only when a well-formed tool_use block comes back.
// A transport error surfaces as (false, err) — unreachable is not the same as
// incapable. Callers cache the result per session; the probe is stateless.
func Probe(ctx context.Context, prov Provider) (bool, error) {
	req := ChatRequest{
		System:   `You are a tool-use capability probe. Call the echo tool with text set to "ping". Respond ONLY by calling the tool — no prose.`,
		Messages: []Message{TextMessage(RoleUser, "Call the echo tool now.")},
		Tools: []Tool{{
			Name:        probeToolName,
			Description: "Echoes back the provided text.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		}},
		// Generous cap: local reasoning models burn tokens thinking before the call.
		MaxTokens: 512,
	}
	resp, err := prov.Chat(ctx, req)
	if err != nil {
		return false, err
	}
	for _, tu := range resp.ToolUses() {
		if tu.Name == probeToolName && json.Valid(tu.Input) {
			return true, nil
		}
	}
	return false, nil
}

// AdvertisesTools reports whether prov lists model with native tool support.
// Advertised (Anthropic, Gemini) → callers skip the live probe: no extra call,
// no launch latency, no API spend. False/unlisted (OpenAI-compatible listings
// can't attest tools) → probe to find out.
func AdvertisesTools(ctx context.Context, prov Provider, model string) bool {
	ms, err := prov.Models(ctx)
	if err != nil {
		return false
	}
	for _, m := range ms {
		if m.ID == model && m.Tools {
			return true
		}
	}
	return false
}
