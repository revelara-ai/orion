package decomposer

import (
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// or-jh4: CoverageGate asserts every declared behavioral CASE id (not just the
// functional dimension) is owned by a task — so a decomposition that drops a
// specific case is caught at plan time.
func TestCoverageGateCaseIDCoverage(t *testing.T) {
	es := acceptedSpec(t)
	es.ResponseContract.Cases = []spec.BehavioralCase{
		{ID: "case1", Request: spec.RequestShape{Method: "GET", Path: "/time"}, Expect: spec.ExpectShape{Status: 200}},
		{ID: "case2", Request: spec.RequestShape{Method: "GET", Path: "/time"}, Expect: spec.ExpectShape{Status: 400}},
	}

	epic := Decompose(es, "http-service")
	if err := CoverageGate(es, epic); err != nil {
		t.Fatalf("the decomposed plan should own every declared case id: %v", err)
	}

	// Tamper: a plan whose tasks cover the functional DIMENSION but drop a specific
	// case must be REJECTED.
	for i := range epic.Tasks {
		if epic.Tasks[i].Key == "handler" {
			epic.Tasks[i].Covers = []string{string(completeness.DimFunctional)}
		}
	}
	if err := CoverageGate(es, epic); err == nil {
		t.Fatal("CoverageGate must reject a plan that drops a declared behavioral case")
	}
}

// A spec with no behavioral cases is vacuously covered — case-less specs are not
// broken by the new check (the requirement to declare cases is enforced elsewhere).
func TestCoverageGateNoCasesVacuous(t *testing.T) {
	es := acceptedSpec(t) // compiled with nil requirements → zero cases
	if err := CoverageGate(es, Decompose(es, "http-service")); err != nil {
		t.Fatalf("a case-less spec must not fail coverage: %v", err)
	}
}
