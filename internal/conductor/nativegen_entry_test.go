package conductor

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// The generator is instructed to expose the DECLARED entry symbol, not a hardwired
// handleTime (or-3ba.4).
func TestGenerationRoleUsesDeclaredEntrySymbol(t *testing.T) {
	role := generationRole(sandbox.GenSpec{Route: "/now", Port: 8080, EntrySymbol: "handleNow"})
	if !strings.Contains(role, "handleNow") {
		t.Fatalf("generation role does not instruct the declared entry symbol:\n%s", role)
	}
	if strings.Contains(role, "handleTime") {
		t.Fatalf("generation role still hardwires handleTime:\n%s", role)
	}
}

// The generator's default entry symbol and the proof corpus' default MUST match, or
// the generated code and the harness-authored corpus would call different symbols
// and never compile together. This guards the two from drifting.
func TestGeneratorAndProofShareDefaultEntrySymbol(t *testing.T) {
	if got := (sandbox.GenSpec{}).Entry(); got != testsynth.DefaultEntrySymbol {
		t.Fatalf("GenSpec default entry %q != testsynth.DefaultEntrySymbol %q — generator and corpus would disagree",
			got, testsynth.DefaultEntrySymbol)
	}
}
