package completeness

import (
	"testing"

	"github.com/revelara-ai/orion/internal/lang"
)

// TestLanguageCapabilityFromRegistry (or-4y7.1): direction.language's provable
// set is sourced from the language registry, not a literal — so a registered
// language (go) is provable and an unregistered one (python) is still refused.
// Registering a language is the ONLY way to make it provable.
func TestLanguageCapabilityFromRegistry(t *testing.T) {
	provable, ok := provableFor("direction.language")
	if !ok {
		t.Fatal("direction.language must be a constrained (provable) direction")
	}
	// It IS the registry — not a hardcoded {go}.
	reg := lang.Registered()
	if len(provable) != len(reg) {
		t.Fatalf("direction.language provable set must equal lang.Registered(): %v vs %v", provable, reg)
	}
	for i := range reg {
		if provable[i] != reg[i] {
			t.Fatalf("provable set diverged from the registry: %v vs %v", provable, reg)
		}
	}

	// A registered language passes the gap gate; an unregistered one is a gap.
	if g := DirectionGaps(map[string]string{"direction.language": "go"}); len(g) != 0 {
		t.Fatalf("go is registered → no gap, got %+v", g)
	}
	pyGaps := DirectionGaps(map[string]string{"direction.language": "python"})
	if len(pyGaps) != 1 || pyGaps[0].Key != "direction.language" {
		t.Fatalf("python is not registered → must be a capability gap, got %+v", pyGaps)
	}
	// The other constrained rows stay static (not registry-driven).
	if g := DirectionGaps(map[string]string{"direction.engine": "unreal"}); len(g) != 1 {
		t.Fatalf("a non-provable engine must still gap, got %+v", g)
	}
}
