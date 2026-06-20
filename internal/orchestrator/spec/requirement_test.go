package spec

import (
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

func tzRequirement() Requirement {
	return Requirement{
		Source:      completeness.DimFunctional,
		DecisionKey: "timezone",
		Text:        "default UTC; ?tz=zone returns that zone; invalid tz → 400 json error",
		Cases: []BehavioralCase{
			{
				Request: RequestShape{Method: "GET", Path: "/time", Query: map[string]string{"tz": "America/New_York"}},
				Expect:  ExpectShape{Status: 200, ContentType: "application/json", Assertions: []BodyAssertion{{Kind: AssertJSONKeyInTZ, Key: "time", Value: "America/New_York"}}},
			},
			{
				Request: RequestShape{Method: "GET", Path: "/time", Query: map[string]string{"tz": "Bogus/Zone"}},
				Expect:  ExpectShape{Status: 400, ContentType: "application/json", Assertions: []BodyAssertion{{Kind: AssertJSONErrorPresent}}},
			},
		},
	}
}

// TestScalarOnlySpecHashIgnoresDerivedCases is the backward-compat anchor proof: a
// scalar-only spec carries a derived default case, but its hash must be invariant
// to that case (so already-anchored legacy specs — which had no Cases field — hash
// identically and still pass RecallSpec).
func TestScalarOnlySpecHashIgnoresDerivedCases(t *testing.T) {
	checklist := completeness.NewAnalyzer("http-service").Checklist()
	s, err := Compile("intent", fullAnswers(), nil, checklist, nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(s.ResponseContract.Cases) == 0 {
		t.Fatal("expected a derived default case in the contract")
	}
	if len(s.Requirements) != 0 {
		t.Fatal("scalar-only spec must have no requirements")
	}
	// Clearing the derived cases must not change the anchor.
	bare := s
	bare.ResponseContract.Cases = nil
	if bare.ComputeHash() != s.Hash {
		t.Fatalf("anchor depends on derived cases (legacy specs would break):\n with=%s\n without=%s", s.Hash, bare.ComputeHash())
	}
}

// TestRequirementLowersAndChangesHash: a stated requirement adds its cases to the
// contract and (because the spec's meaning changed) yields a different anchor.
func TestRequirementLowersAndChangesHash(t *testing.T) {
	checklist := completeness.NewAnalyzer("http-service").Checklist()
	scalar, err := Compile("intent", fullAnswers(), nil, checklist, nil)
	if err != nil {
		t.Fatalf("compile scalar: %v", err)
	}
	withReq, err := Compile("intent", fullAnswers(), nil, checklist, []Requirement{tzRequirement()})
	if err != nil {
		t.Fatalf("compile with requirement: %v", err)
	}
	// default case + 2 requirement cases = 3.
	if len(withReq.ResponseContract.Cases) != 3 {
		t.Fatalf("cases = %d, want 3 (default + 2)", len(withReq.ResponseContract.Cases))
	}
	if len(withReq.Requirements) != 1 || withReq.Requirements[0].ID == "" {
		t.Fatalf("requirement not anchored with an id: %+v", withReq.Requirements)
	}
	if withReq.Hash == scalar.Hash {
		t.Fatal("a spec with a behavioral requirement must anchor differently than the scalar-only spec")
	}
	// Every case has a stable id (the obligation key).
	for _, c := range withReq.ResponseContract.Cases {
		if c.ID == "" {
			t.Fatalf("case missing id: %+v", c)
		}
	}
}

// TestCompileRejectsUnprovableRequirement: a requirement the proof can't execute
// must fail at compile — never anchored (the or-y9d invariant at the first gate).
func TestCompileRejectsUnprovableRequirement(t *testing.T) {
	checklist := completeness.NewAnalyzer("http-service").Checklist()
	bad := []Requirement{
		{Text: "no cases"}, // zero cases
		{Text: "unknown kind", Cases: []BehavioralCase{{Request: RequestShape{Method: "GET", Path: "/t"}, Expect: ExpectShape{Status: 200, ContentType: "application/json", Assertions: []BodyAssertion{{Kind: "bogus"}}}}}},
		{Text: "bad zone", Cases: []BehavioralCase{{Request: RequestShape{Method: "GET", Path: "/t"}, Expect: ExpectShape{Status: 200, ContentType: "application/json", Assertions: []BodyAssertion{{Kind: AssertJSONKeyInTZ, Key: "time", Value: "Not/AZone"}}}}}},
		{Text: "xml", Cases: []BehavioralCase{{Request: RequestShape{Method: "GET", Path: "/t"}, Expect: ExpectShape{Status: 200, ContentType: "application/xml"}}}},
	}
	for _, r := range bad {
		if _, err := Compile("intent", fullAnswers(), nil, checklist, []Requirement{r}); err == nil {
			t.Errorf("requirement %q should fail compile, but it anchored", r.Text)
		}
	}
}
