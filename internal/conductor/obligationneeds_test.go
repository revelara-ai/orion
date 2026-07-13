package conductor

import (
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
)

// or-gb1.1: the E2.5 gate's needs derivation is conservative and exact —
// tz obligations and unit package surfaces, deduplicated.
func TestObligationNeedsDerivation(t *testing.T) {
	c := testsynth.Contract{
		TimeZone: "UTC",
		Cases: []spec.BehavioralCase{
			{Expect: spec.ExpectShape{Assertions: []spec.BodyAssertion{
				{Kind: spec.AssertJSONKeyInTZ, Key: "now", Value: "America/New_York"},
				{Kind: spec.AssertJSONKeyPresent, Key: "now"}, // no evidence need
			}}},
			{Kind: spec.KindUnit, Unit: &spec.UnitCase{Pkg: "storage", Steps: []spec.UnitStep{{Call: "Put()", Want: "1"}}}},
			{Kind: spec.KindUnit, Unit: &spec.UnitCase{Pkg: "storage", Steps: []spec.UnitStep{{Call: "Get()", Want: "1"}}}}, // dup pkg
		},
	}
	needs := obligationNeeds(c)
	want := map[string]bool{"America/New_York": true, "storage": true, "UTC": true}
	if len(needs) != len(want) {
		t.Fatalf("needs must be exact + deduped: %v", needs)
	}
	for _, n := range needs {
		if !want[n] {
			t.Fatalf("unexpected need %q in %v", n, needs)
		}
	}
}
