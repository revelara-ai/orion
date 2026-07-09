package harness

import (
	"strings"

	"github.com/revelara-ai/orion/pkg/llm"
)

// leaksToolCallSyntax reports whether an end-of-turn text contains a leaked
// tool invocation: a model regressing to its trained chat template (the
// Hermes-style <tool_call>/<function=...> XML Qwen-family models emit under
// deep context) instead of the function-calling API. Both markers are
// required — prose that merely MENTIONS one of the tags (e.g. Orion working
// on this very file) must not trip the guard.
func leaksToolCallSyntax(text string) bool {
	return strings.Contains(text, "<tool_call>") && strings.Contains(text, "<function=")
}

// toolNames renders the registry's tool names for the corrective message,
// bounded so a 40-tool registry doesn't bloat the nudge.
func toolNames(specs []llm.Tool) string {
	names := make([]string, 0, len(specs))
	for _, t := range specs {
		names = append(names, t.Name)
		if len(names) == 24 {
			names = append(names, "…")
			break
		}
	}
	return strings.Join(names, ", ")
}

// endsWithAnnouncement reports whether an end-of-turn text trails off with a
// colon — the signature of an announced-but-unemitted action ("Let me submit
// the intent:"). Markdown emphasis and whitespace around the tail are ignored.
func endsWithAnnouncement(text string) bool {
	t := strings.TrimRight(strings.TrimSpace(text), "*_ \t")
	return strings.HasSuffix(t, ":")
}
