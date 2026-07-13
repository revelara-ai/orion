package conductor

import (
	"errors"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// A rejected unit case must keep the precise anchor diagnosis AND teach: the
// closed union, a minimal valid unit example, and the test-only steer to the
// brownfield change flow (the gemma token-furnace fix, or-4j37).
func TestTeachCaseShapeUnitRejectionTeaches(t *testing.T) {
	cases := []spec.BehavioralCase{{
		Kind: spec.KindUnit,
		Unit: &spec.UnitCase{Steps: []spec.UnitStep{{Call: `Put("k","v")`, Want: "error(nil)", WantErrRE: "boom"}}},
	}}
	verr := spec.ValidateRequirement(spec.Requirement{Text: "t", Cases: cases})
	if verr == nil {
		t.Fatal("fixture must fail validation (want and want_err_re both set)")
	}
	got := teachCaseShape(verr, cases)
	for _, must := range []string{
		"exactly one of want / want_err_re", // the anchor diagnosis for THIS payload
		"CLOSED UNION",
		`"kind":"unit"`,        // a minimal valid example
		"submit_change_intent", // the test-only steer
	} {
		if !strings.Contains(got.Error(), must) {
			t.Fatalf("teaching error missing %q:\n%s", must, got.Error())
		}
	}
	if !errors.Is(got, verr) && errors.Unwrap(got) == nil {
		t.Fatal("original validation error must stay unwrappable")
	}
}

// An http rejection teaches the http shape — and must NOT steer to the change
// flow (a greenfield service intent mis-steered to brownfield is a new furnace).
func TestTeachCaseShapeHTTPRejectionNoSteer(t *testing.T) {
	cases := []spec.BehavioralCase{{Request: spec.RequestShape{Method: "GET"}}} // missing path
	verr := spec.ValidateRequirement(spec.Requirement{Text: "t", Cases: cases})
	if verr == nil {
		t.Fatal("fixture must fail validation (missing path)")
	}
	got := teachCaseShape(verr, cases)
	if !strings.Contains(got.Error(), "CLOSED UNION") || !strings.Contains(got.Error(), `"path":"/time"`) {
		t.Fatalf("http rejection must still teach union + http example:\n%s", got.Error())
	}
	if strings.Contains(got.Error(), "submit_change_intent") {
		t.Fatalf("http rejection must not steer to the change flow:\n%s", got.Error())
	}
}

// An unknown kind names the union and steers (unknown kinds are where
// test-only payload inventions land).
func TestTeachCaseShapeUnknownKindSteers(t *testing.T) {
	cases := []spec.BehavioralCase{{Kind: spec.CaseKind("gotest")}}
	verr := spec.ValidateRequirement(spec.Requirement{Text: "t", Cases: cases})
	if verr == nil {
		t.Fatal("fixture must fail validation (unknown kind)")
	}
	got := teachCaseShape(verr, cases)
	if !strings.Contains(got.Error(), "submit_change_intent") || !strings.Contains(got.Error(), `"kind":"unit"`) {
		t.Fatalf("unknown kind must teach the unit shape and steer:\n%s", got.Error())
	}
}

func TestTeachCaseShapeNilPassthrough(t *testing.T) {
	if teachCaseShape(nil, nil) != nil {
		t.Fatal("nil error must stay nil")
	}
}
