package llm

import (
	"context"
	"errors"
	"strings"
)

// ErrContextOverflow marks a request rejected because the assembled prompt
// exceeds the model's context window (e.g. Anthropic's 400 "prompt is too
// long"). It is a SENTINEL the harness branches on to shrink-and-retry rather
// than surface a terminal failure that bricks the session. Wrap it with %w.
var ErrContextOverflow = errors.New("llm: context window exceeded")

// TokenCounter is an OPTIONAL provider capability: an exact, provider-native
// token count for a request (Anthropic's /v1/messages/count_tokens). The context
// manager type-asserts for it and falls back to EstimateTokens when absent — so
// the narrow Provider interface stays unchanged and local/offline brains that
// can't count still work. Mirrors the ErrNotSupported degradation philosophy.
type TokenCounter interface {
	CountTokens(ctx context.Context, req ChatRequest) (int, error)
}

// ContextWindow is an OPTIONAL provider capability: the model's context window in
// tokens. Providers that report it are governed precisely; those that don't fall
// back to a conservative default (see internal/contextwindow). A cheap local
// model with an 8K window and a 1M Anthropic brain are governed by the same code.
type ContextWindow interface {
	ContextWindow() int
}

// MaxOutputTokens is an OPTIONAL provider capability: the model's max OUTPUT cap
// (distinct from, and far below, the context window). The harness bounds its
// requested max_tokens by this so it never asks for more output than the model
// allows — which the provider rejects with an unrecoverable 400.
type MaxOutputTokens interface {
	MaxOutputTokens() int
}

// CountOrEstimate returns the request's input-token size, preferring the
// provider's exact TokenCounter when available and degrading to the heuristic
// estimate otherwise (or when the exact count errors / returns nonsense).
//
// This is the sensor's exact-count extension point. Today no provider implements
// TokenCounter, so it degrades to EstimateTokens everywhere; the Anthropic
// count_tokens implementation and the cost-aware wiring into the harness trigger
// checks land in the "native fast-path" slice (deliberately deferred there so the
// per-iteration hot path isn't committed to a network round-trip prematurely).
func CountOrEstimate(ctx context.Context, prov Provider, req ChatRequest) int {
	if tc, ok := prov.(TokenCounter); ok {
		if n, err := tc.CountTokens(ctx, req); err == nil && n > 0 {
			return n
		}
	}
	return EstimateTokens(req)
}

// EstimateTokens is a cheap, provider-agnostic approximation of a request's
// input-token count (~chars/4, the standard rough heuristic). It counts the
// system prompt and EVERY content block's payload — text, tool_use input, and
// tool_result bodies — since tool results are the dominant source of context
// bloat. It is a gauge for WHEN to shrink, never an accounting source of truth.
func EstimateTokens(req ChatRequest) int {
	chars := len(req.System)
	for _, m := range req.Messages {
		for _, b := range m.Content {
			chars += len(b.Text)
			if b.ToolUse != nil {
				chars += len(b.ToolUse.Name) + len(b.ToolUse.Input)
			}
			if b.ToolResult != nil {
				chars += len(b.ToolResult.Content)
			}
		}
	}
	// Tool specs are serialized into every request and counted by the provider —
	// on a small local window the tool footprint alone can exceed the whole budget,
	// so the gauge MUST include them or proactive clearing never fires in time.
	for _, t := range req.Tools {
		chars += len(t.Name) + len(t.Description) + len(t.InputSchema)
	}
	return chars / 4
}

// isContextOverflow reports whether a provider error body indicates the prompt
// exceeded the context window (vs any other 4xx, which must NOT be treated as
// recoverable-by-shrinking).
func isContextOverflow(body string) bool {
	b := strings.ToLower(body)
	for _, marker := range []string{
		"prompt is too long",
		"prompt too long",
		"context window",
		"context length",
		"maximum context",
		"too many tokens",
		"exceeds the maximum number of tokens",
	} {
		if strings.Contains(b, marker) {
			return true
		}
	}
	return false
}
