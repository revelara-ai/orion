package proofexec

import (
	"strings"
	"testing"
)

// or-fvkm: the manifest is the proof env's self-description for the agents
// that generate code proven under it (CAST F2: the generator had NO
// representation of these constraints and burned three attempts against
// them). It must stay consistent with what toolEnv actually enforces.
func TestCapabilityManifestMatchesEnv(t *testing.T) {
	m := CapabilityManifest()
	env := toolEnv("/goroot", "/work")

	if env["GOPROXY"] == "off" && !strings.Contains(m, "network") {
		t.Error("GOPROXY=off must be described (network denied)")
	}
	if env["CGO_ENABLED"] == "0" && !strings.Contains(m, "CGO") {
		t.Error("CGO_ENABLED=0 must be described")
	}
	for _, want := range []string{"protoc", "generation time", "GOPROXY=off"} {
		if !strings.Contains(m, want) {
			t.Errorf("manifest must mention %q:\n%s", want, m)
		}
	}
	if len(m) > 1500 {
		t.Errorf("the manifest rides in prompts — keep it tight, got %d bytes", len(m))
	}
}
