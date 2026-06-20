package conductor

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// TestBuildEscalatesUnsatisfiedRequirement is the or-y9d regression: a spec that
// states a tz-param + 400 behavior (as structured requirements) is ratified and
// built with the fixture generator — which does NOT implement tz. The tz cases
// execute and fail, so the build must NOT converge to Accept and must NOT close
// the task. "Proven" reflects the spec, not just the happy path.
func TestBuildEscalatesUnsatisfiedRequirement(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a service + proof")
	}
	oc := orchestrator.NewWithStore(openStore(t))
	ctx := context.Background()
	if _, err := oc.Submit(ctx, "Build an HTTP service that returns the current time."); err != nil {
		t.Fatalf("submit: %v", err)
	}
	for _, a := range [][2]string{{"response_format", "json"}, {"timezone", "UTC"}, {"port", "8080"}, {"route", "/time"}} {
		if err := oc.RecordAnswer(ctx, a[0], a[1]); err != nil {
			t.Fatalf("answer %s: %v", a[0], err)
		}
	}
	// The developer's richer requirement, decomposed into verifiable cases.
	req := spec.Requirement{
		Source:      completeness.DimFunctional,
		DecisionKey: "timezone",
		Text:        "?tz=zone returns that zone; invalid tz → 400 json error",
		Cases: []spec.BehavioralCase{
			{Request: spec.RequestShape{Method: "GET", Path: "/time", Query: map[string]string{"tz": "America/New_York"}}, Expect: spec.ExpectShape{Status: 200, ContentType: "application/json", Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyInTZ, Key: "time", Value: "America/New_York"}}}},
			{Request: spec.RequestShape{Method: "GET", Path: "/time", Query: map[string]string{"tz": "Bogus/Zone"}}, Expect: spec.ExpectShape{Status: 400, ContentType: "application/json", Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONErrorPresent}}}},
		},
	}
	if err := oc.AddRequirement(ctx, req); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	// Ratification must succeed (the requirement is lowerable) AND the persistence
	// round-trip must hold (RecallSpec re-derives the same anchor).
	if _, err := oc.ApproveSpec(ctx); err != nil {
		t.Fatalf("approve: %v", err)
	}

	res, err := BuildAndProve(ctx, oc.Store(), nil, nil) // nil gen = fixture (ignores tz)
	if err != nil {
		t.Fatalf("build (must run, not error — incl. anchor round-trip): %v", err)
	}
	if res.Verdict == "Accept" || res.Closed {
		t.Fatalf("a spec requirement the build does not satisfy must NOT be Accepted/closed: %+v", res)
	}
	if res.Delivery != "escalate" {
		t.Fatalf("delivery = %q, want escalate (unproven requirement)", res.Delivery)
	}
}
