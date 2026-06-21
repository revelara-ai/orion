package spec

import (
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

func fullAnswers() map[string]string {
	return map[string]string{
		"response_format":       "json",
		"timezone":              "UTC",
		"port":                  "8080",
		"route":                 "/time",
		"scale_profile":         "medium",
		"observability_signals": "logs",
		"oncall_escalation":     "single owner",
		"data_storage":          "none",
		"slo_targets":           "tier-default",
		"security_model":        "untrusted input",
		"dependencies":          "none",
	}
}

// TestCompileResponseContractFromDecisions: compiling complete decisions yields a
// machine-checkable ResponseContract that reflects the approved decisions.
func TestCompileResponseContractFromDecisions(t *testing.T) {
	checklist := completeness.NewAnalyzer("http-service").Checklist()
	s, err := Compile("Build an HTTP service that returns the current time.", fullAnswers(), nil, checklist, nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	rc := s.ResponseContract
	if rc.ContentType != "application/json" {
		t.Fatalf("content type = %q, want application/json", rc.ContentType)
	}
	if rc.Route != "/time" || rc.Port != 8080 || rc.TimeZone != "UTC" {
		t.Fatalf("contract did not reflect decisions: %+v", rc)
	}
	// The JSON contract schema is a GENERIC object — it must NOT hardcode a "time"
	// property (that imposed the time domain on every JSON service). Specific shape is
	// declared via requirements/cases.
	if rc.Schema["type"] != "object" {
		t.Fatalf("JSON contract schema should be a generic object: %+v", rc.Schema)
	}
	if _, hasProps := rc.Schema["properties"]; hasProps {
		t.Fatalf("JSON contract schema must not hardcode properties like 'time': %+v", rc.Schema)
	}
	if s.Hash == "" || !s.VerifyAnchor() {
		t.Fatalf("spec hash not anchored/verifiable: %q", s.Hash)
	}
}

// TestDefaultCaseHasNoTimeAssumption: a compiled spec's default happy-path case
// asserts only status + content-type — it must NOT assume a "time" key or an RFC3339
// body. Body shape is declared via requirements, never imposed by the harness (the
// general-harness fix: a non-time service is not held to return a timestamp).
func TestDefaultCaseHasNoTimeAssumption(t *testing.T) {
	checklist := completeness.NewAnalyzer("http-service").Checklist()
	s, err := Compile("Build an HTTP service.", fullAnswers(), nil, checklist, nil)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(s.ResponseContract.Cases) == 0 {
		t.Fatal("expected a default case")
	}
	for _, c := range s.ResponseContract.Cases {
		for _, a := range c.Expect.Assertions {
			if a.Key == "time" || a.Kind == AssertJSONKeyRFC3339 || a.Kind == AssertBodyRFC3339 {
				t.Fatalf("default case still assumes a time/RFC3339 body: %+v", a)
			}
		}
	}
}

// TestCompileRejectsIncompleteDecisions: a missing decision is a hard error (the
// spec never compiles from an incomplete set).
func TestCompileRejectsIncompleteDecisions(t *testing.T) {
	checklist := completeness.NewAnalyzer("http-service").Checklist()
	a := fullAnswers()
	delete(a, "port")
	if _, err := Compile("intent", a, nil, checklist, nil); err == nil {
		t.Fatal("expected error compiling with a missing decision")
	}
}

// TestHashIsDeterministicAndContentSensitive: the anchor is stable for identical
// content and changes when content changes.
func TestHashIsDeterministicAndContentSensitive(t *testing.T) {
	checklist := completeness.NewAnalyzer("http-service").Checklist()
	s1, _ := Compile("intent", fullAnswers(), nil, checklist, nil)
	s2, _ := Compile("intent", fullAnswers(), nil, checklist, nil)
	if s1.Hash != s2.Hash {
		t.Fatalf("hash not deterministic: %s vs %s", s1.Hash, s2.Hash)
	}
	a := fullAnswers()
	a["port"] = "9090"
	s3, _ := Compile("intent", a, nil, checklist, nil)
	if s3.Hash == s1.Hash {
		t.Fatal("hash did not change when a decision changed")
	}
}

// TestVerifyAnchorDetectsTamper: mutating spec content without recomputing the
// hash fails anchor verification.
func TestVerifyAnchorDetectsTamper(t *testing.T) {
	checklist := completeness.NewAnalyzer("http-service").Checklist()
	s, _ := Compile("intent", fullAnswers(), nil, checklist, nil)
	if !s.VerifyAnchor() {
		t.Fatal("freshly compiled spec should verify")
	}
	s.Decisions["port"] = "1" // tamper without rehashing
	if s.VerifyAnchor() {
		t.Fatal("anchor verification must fail after tampering")
	}
}
