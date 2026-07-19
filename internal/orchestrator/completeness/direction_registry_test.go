package completeness

import (
	"testing"

	"github.com/revelara-ai/orion/internal/lang"
)

// TestLanguageCapabilityFromRegistry (or-4y7.1): direction.language's provable
// set is sourced from the language registry, not a literal — so a registered
// language (go, and python since or-4y7.9) is provable and an unregistered one
// (ruby) is still refused.
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
	pyGaps := DirectionGaps(map[string]string{"direction.language": "ruby"})
	if len(pyGaps) != 1 || pyGaps[0].Key != "direction.language" {
		t.Fatalf("ruby is not registered → must be a capability gap, got %+v", pyGaps)
	}
	// The other constrained rows stay static (not registry-driven).
	if g := DirectionGaps(map[string]string{"direction.engine": "unreal"}); len(g) != 1 {
		t.Fatalf("a non-provable engine must still gap, got %+v", g)
	}
}

// TestSplitLanguageRuntime (or-4y7.10): a direction.language answer may carry
// the developer's runtime pin; the base language drives capability.
func TestSplitLanguageRuntime(t *testing.T) {
	for in, want := range map[string][2]string{
		"python":        {"python", ""},
		"python 3.12":   {"python", "3.12"},
		"python3.12":    {"python", "3.12"},
		"Python@3.12.4": {"python", "3.12.4"},
		"go 1.22":       {"go", "1.22"},
		"go":            {"go", ""},
	} {
		l, v := SplitLanguageRuntime(in)
		if l != want[0] || v != want[1] {
			t.Errorf("SplitLanguageRuntime(%q) = (%q, %q), want %v", in, l, v, want)
		}
	}
}

// TestVersionedLanguageAnswerIsProvable (or-4y7.10): "python 3.12" is judged by
// its base language — no capability gap; an unregistered base still gaps.
func TestVersionedLanguageAnswerIsProvable(t *testing.T) {
	if gaps := DirectionGaps(map[string]string{"direction.language": "python 3.12"}); len(gaps) != 0 {
		t.Fatalf("a versioned registered language must not gap, got %+v", gaps)
	}
	if gaps := DirectionGaps(map[string]string{"direction.language": "ruby 3.3"}); len(gaps) != 1 {
		t.Fatalf("a versioned UNregistered language must still gap, got %+v", gaps)
	}
}
