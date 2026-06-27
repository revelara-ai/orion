package conductor

import (
	"strings"
	"testing"
)

// TestRoleRenderHasWorkflow (or-gik.5): the conductor role priming carries the "Orion workflow"
// section + the explain-on-ask instruction, so the conductor can describe the opinionated loop
// and its gates when a developer asks — without losing the hard proof invariant.
func TestRoleRenderHasWorkflow(t *testing.T) {
	out := RoleTemplate{Project: "orion"}.Render()
	for _, want := range []string{
		"The Orion workflow", "adversarial grill", "ratified spec", "build_service",
		"behavioral", "empirical", "hazard", "CONVERGE", "explain this concisely",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("role prompt missing workflow element %q", want)
		}
	}
	if !strings.Contains(out, "never a substitute for proof") {
		t.Error("role prompt must retain the proof invariant")
	}
}
