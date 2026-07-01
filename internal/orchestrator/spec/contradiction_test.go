package spec

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

// compileWithCases compiles a minimal non-HTTP spec (no synthesized default case)
// carrying one requirement with the given cases.
func compileWithCases(t *testing.T, cases []BehavioralCase) (ExecutableSpec, error) {
	t.Helper()
	return Compile("test intent", map[string]string{}, map[string]string{}, nil,
		[]Requirement{{Source: completeness.DimFunctional, Text: "behavior under test", Cases: cases}})
}

func caseOn(path string, status int, ct string, asserts ...BodyAssertion) BehavioralCase {
	return BehavioralCase{
		Request: RequestShape{Method: "GET", Path: path},
		Expect:  ExpectShape{Status: status, ContentType: ct, Assertions: asserts},
	}
}

// TestCompileBlocksSameRequestDifferentStatus: the canonical contradiction — one
// request cannot be required to return two statuses. It must fail COMPILE (before
// anchor, before any code), not surface as a post-generation proof failure.
func TestCompileBlocksSameRequestDifferentStatus(t *testing.T) {
	_, err := compileWithCases(t, []BehavioralCase{
		caseOn("/x", 200, "application/json"),
		caseOn("/x", 404, "application/json"),
	})
	if err == nil {
		t.Fatal("contradictory statuses for one request must not compile")
	}
	if !strings.Contains(err.Error(), "contradict") {
		t.Errorf("error should name the contradiction, got: %v", err)
	}
}

// TestCompileBlocksSameRequestDifferentContentType: one request, two content types.
func TestCompileBlocksSameRequestDifferentContentType(t *testing.T) {
	_, err := compileWithCases(t, []BehavioralCase{
		caseOn("/x", 200, "application/json"),
		caseOn("/x", 200, "text/plain"),
	})
	if err == nil {
		t.Fatal("contradictory content types for one request must not compile")
	}
}

// TestCompileBlocksRawBodyVsJSONBody: a body cannot be both a raw RFC3339 string
// and a JSON document.
func TestCompileBlocksRawBodyVsJSONBody(t *testing.T) {
	_, err := compileWithCases(t, []BehavioralCase{
		caseOn("/t", 200, "", BodyAssertion{Kind: AssertBodyRFC3339}),
		caseOn("/t", 200, "", BodyAssertion{Kind: AssertJSONKeyPresent, Key: "time"}),
	})
	if err == nil {
		t.Fatal("raw-RFC3339 body and JSON body on one request must not compile")
	}
}

// TestCompileBlocksTwoZonesOneKey: the same JSON key cannot be required in two
// different timezones.
func TestCompileBlocksTwoZonesOneKey(t *testing.T) {
	_, err := compileWithCases(t, []BehavioralCase{
		caseOn("/t", 200, "", BodyAssertion{Kind: AssertJSONKeyInTZ, Key: "time", Value: "UTC"}),
		caseOn("/t", 200, "", BodyAssertion{Kind: AssertJSONKeyInTZ, Key: "time", Value: "America/New_York"}),
	})
	if err == nil {
		t.Fatal("two zones for one key on one request must not compile")
	}
}

// TestCompileBlocksContractVsRequirementConflict: the synthesized default case
// (from the scalar contract) participates — a requirement demanding a different
// status for the contract's own happy path is the "answers say X, case says not-X"
// class the audit flagged.
func TestCompileBlocksContractVsRequirementConflict(t *testing.T) {
	answers := map[string]string{"response_format": "json", "timezone": "UTC", "port": "8080", "route": "/time"}
	checklist := []completeness.RequiredDecision{}
	_, err := Compile("time service", answers, map[string]string{}, checklist,
		[]Requirement{{Source: completeness.DimFunctional, Text: "conflicting happy path", Cases: []BehavioralCase{
			caseOn("/time", 500, "application/json"),
		}}})
	if err == nil {
		t.Fatal("a requirement contradicting the contract's default case must not compile")
	}
}

// TestCompileAllowsCompatibleCases: no false positives — refining assertions on one
// request (presence + format of the same key) and distinct requests (different
// query) are both legitimate.
func TestCompileAllowsCompatibleCases(t *testing.T) {
	if _, err := compileWithCases(t, []BehavioralCase{
		caseOn("/t", 200, "application/json",
			BodyAssertion{Kind: AssertJSONKeyPresent, Key: "time"},
			BodyAssertion{Kind: AssertJSONKeyRFC3339, Key: "time"}),
		{
			Request: RequestShape{Method: "GET", Path: "/t", Query: map[string]string{"tz": "bogus"}},
			Expect:  ExpectShape{Status: 400, ContentType: "application/json", Assertions: []BodyAssertion{{Kind: AssertJSONErrorPresent}}},
		},
	}); err != nil {
		t.Fatalf("compatible cases must compile: %v", err)
	}
}

// TestFindContradictionsNamesBothCases: the report carries both case ids and the
// request so the developer decision is concrete.
func TestFindContradictionsNamesBothCases(t *testing.T) {
	a, b := caseOn("/x", 200, ""), caseOn("/x", 404, "")
	a.EnsureID()
	b.EnsureID()
	cs := FindContradictions([]BehavioralCase{a, b})
	if len(cs) != 1 {
		t.Fatalf("want exactly one contradiction, got %d: %+v", len(cs), cs)
	}
	got := cs[0]
	if got.CaseA == "" || got.CaseB == "" || !strings.Contains(got.Request, "/x") || got.Reason == "" {
		t.Errorf("contradiction must name both cases, the request, and the reason: %+v", got)
	}
}
