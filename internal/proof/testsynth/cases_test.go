package testsynth

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// TestPerCaseCorpusCompiles: the generated corpus must be syntactically valid Go
// for EVERY assertion kind (a codegen bug would otherwise only surface at proof
// time as a corpus build failure).
func TestPerCaseCorpusCompiles(t *testing.T) {
	cases := []spec.BehavioralCase{
		{ID: "present", Request: spec.RequestShape{Method: "GET", Path: "/x"}, Expect: spec.ExpectShape{Status: 200, ContentType: "application/json", Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyPresent, Key: "message"}}}},
		{ID: "rfc", Request: spec.RequestShape{Method: "GET", Path: "/x"}, Expect: spec.ExpectShape{Status: 200, ContentType: "application/json", Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyRFC3339, Key: "time"}}}},
		{ID: "tz", Request: spec.RequestShape{Method: "GET", Path: "/x", Query: map[string]string{"tz": "America/New_York"}}, Expect: spec.ExpectShape{Status: 200, ContentType: "application/json", Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyInTZ, Key: "time", Value: "America/New_York"}}}},
		{ID: "err", Request: spec.RequestShape{Method: "POST", Path: "/x", Body: "{}"}, Expect: spec.ExpectShape{Status: 400, ContentType: "application/json", Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONErrorPresent}}}},
		{ID: "text", Request: spec.RequestShape{Method: "GET", Path: "/x"}, Expect: spec.ExpectShape{Status: 200, ContentType: "text/plain", Assertions: []spec.BodyAssertion{{Kind: spec.AssertBodyRFC3339}}}},
	}
	corpus := SynthesizeBehavioral(Contract{Route: "/x", Cases: cases})
	if _, err := parser.ParseFile(token.NewFileSet(), "orion_behavioral_test.go", corpus, parser.AllErrors); err != nil {
		t.Fatalf("generated corpus is not valid Go: %v\n---\n%s", err, corpus)
	}
}

func threeCaseContract() Contract {
	return Contract{Route: "/time", Format: "json", Cases: []spec.BehavioralCase{
		{ID: "def", Request: spec.RequestShape{Method: "GET", Path: "/time"}, Expect: spec.ExpectShape{Status: 200, ContentType: "application/json", Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyRFC3339, Key: "time"}}}},
		{ID: "tzny", Request: spec.RequestShape{Method: "GET", Path: "/time", Query: map[string]string{"tz": "America/New_York"}}, Expect: spec.ExpectShape{Status: 200, ContentType: "application/json", Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyInTZ, Key: "time", Value: "America/New_York"}}}},
		{ID: "tzbad", Request: spec.RequestShape{Method: "GET", Path: "/time", Query: map[string]string{"tz": "Bad"}}, Expect: spec.ExpectShape{Status: 400, ContentType: "application/json", Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONErrorPresent}}}},
	}}
}

// TestSynthesizePerCase: with cases, the corpus emits one marker-bracketed test per
// case with the right assertions; with none, the legacy single-test corpus.
func TestSynthesizePerCase(t *testing.T) {
	corpus := SynthesizeBehavioral(threeCaseContract())
	for _, want := range []string{
		"func Test_obl_def", "func Test_obl_tzny", "func Test_obl_tzbad",
		"ORION_OBLIGATION_RUN:def", "ORION_OBLIGATION_PASS:tzny",
		"tz=America",    // query encoded into the request target
		"w.Code != 400", // the 400 case
		"LoadLocation",  // the in_tz assertion
		`body["error"]`, // the json_error_present assertion
	} {
		if !strings.Contains(corpus, want) {
			t.Errorf("per-case corpus missing %q", want)
		}
	}

	legacy := SynthesizeBehavioral(Contract{Route: "/time", Format: "json"})
	if !strings.Contains(legacy, "TestContractBehavior") || strings.Contains(legacy, "ORION_OBLIGATION") {
		t.Fatal("no-cases contract must produce the legacy single-assertion corpus")
	}
}
