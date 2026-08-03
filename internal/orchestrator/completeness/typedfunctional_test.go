package completeness

import (
	"strings"
	"testing"
)

// TestRegisteredTypesCarryFunctionalDecisions (or-kjcm.2): every registered
// project type contributes at least one BLOCKING functional decision, so no
// registered type can reach spec assembly with nothing the developer must
// answer about what the software does. cli/library/worker previously returned
// nil — the zero-behavioral-cases guard was the only thing preventing a
// vacuous ratification.
func TestRegisteredTypesCarryFunctionalDecisions(t *testing.T) {
	want := map[string][]string{
		"cli":     {"command", "output_format"},
		"library": {"public_api", "io_contract"},
		"worker":  {"trigger", "work_unit"},
	}
	for ptype, keys := range want {
		if !RegisteredProjectType(ptype) {
			t.Fatalf("%s must be a registered project type", ptype)
		}
		ds := NewAnalyzer(ptype).Checklist()
		byKey := map[string]RequiredDecision{}
		blocking := 0
		for _, d := range ds {
			byKey[d.Key] = d
			if d.Dimension == DimFunctional && d.Fallback == "" {
				blocking++
			}
		}
		if blocking == 0 {
			t.Fatalf("%s: no blocking functional decision — a project of this type could ratify without answering anything functional", ptype)
		}
		for _, k := range keys {
			d, ok := byKey[k]
			if !ok {
				t.Fatalf("%s checklist missing %q: %+v", ptype, k, ds)
			}
			if d.Dimension != DimFunctional {
				t.Fatalf("%s/%s must be a functional decision, got %q", ptype, k, d.Dimension)
			}
			if d.Fallback != "" {
				t.Fatalf("%s/%s must be blocking (no fallback), got fallback %q", ptype, k, d.Fallback)
			}
		}
		// No type inherits the HTTP vocabulary.
		for _, h := range []string{"response_format", "port", "route"} {
			if _, ok := byKey[h]; ok {
				t.Fatalf("%s must not carry the HTTP decision %q", ptype, h)
			}
		}
		// The universal reliability floor still rides along.
		for _, u := range []string{"scale_profile", "security_model", "slo_targets"} {
			if _, ok := byKey[u]; !ok {
				t.Fatalf("%s checklist missing the universal dimension %q", ptype, u)
			}
		}
	}
}

// TestUnregisteredTypeStillDirectionCompensated: a type outside the registry
// (a game) keeps the direction-family compensator at standard scale, and gets
// no functional template it has no concept of.
func TestUnregisteredTypeStillDirectionCompensated(t *testing.T) {
	if RegisteredProjectType("game") {
		t.Fatal("game must stay unregistered")
	}
	ds := NewAnalyzerScaled("game", ScaleStandard).Checklist()
	var hasDirection bool
	for _, d := range ds {
		if strings.HasPrefix(d.Key, "direction.") {
			hasDirection = true
		}
		if d.Dimension == DimFunctional {
			t.Fatalf("an unregistered type must not inherit a functional template: %+v", d)
		}
	}
	if !hasDirection {
		t.Fatal("an unregistered type must keep the direction-family compensator")
	}
}
