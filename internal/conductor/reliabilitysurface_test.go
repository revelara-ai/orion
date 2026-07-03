package conductor

import (
	"strings"
	"testing"
)

// The Orion system prompt must tell the agent about the revelara.ai reliability
// surface: that it appears as revelara_* tools when authenticated, that it grounds
// reliability claims there, and — crucially — that when those tools are ABSENT and the
// developer asks for reliability data, it should direct them to /mcp login rather than
// flatly denying the capability exists (the dogfood failure this fixes).
func TestSystemPromptDescribesReliabilitySurface(t *testing.T) {
	a := &OrionAgent{role: RoleTemplate{Project: "demo"}}
	p := a.systemPrompt()
	for _, want := range []string{"revelara_", "/mcp login"} {
		if !strings.Contains(p, want) {
			t.Errorf("systemPrompt missing reliability-surface guidance %q", want)
		}
	}
}
