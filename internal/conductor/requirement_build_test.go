package conductor

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// ratifiedWithRequirement submits + answers the functional decisions, adds the
// given requirement, and ratifies — returning the store-backed conductor.
func ratifiedWithRequirement(t *testing.T, req spec.Requirement) (*orchestrator.Conductor, context.Context) {
	t.Helper()
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
	if err := oc.AddRequirement(ctx, req); err != nil {
		t.Fatalf("add requirement: %v", err)
	}
	if _, err := oc.ApproveSpec(ctx); err != nil {
		t.Fatalf("approve: %v", err)
	}
	return oc, ctx
}

// (The green path for a rich spec is now NATIVE generation — see
// nativegen_test.go TestNativeGeneratorBuildsArbitraryService — not a fixture that
// learns time-service tricks. The fixture stays a thin offline fallback.)

// TestBuildEscalatesUnsatisfiedRequirement: the or-y9d invariant still holds for a
// requirement the fixture CANNOT satisfy — here, a response that must carry a
// "zone" key the fixture never emits. The case executes and fails → not Accept,
// escalate, not closed. "Proven" still means meets-the-spec.
func TestBuildEscalatesUnsatisfiedRequirement(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a service + proof")
	}
	req := spec.Requirement{
		Source: completeness.DimFunctional,
		Text:   "the response must include a non-empty \"zone\" field",
		Cases: []spec.BehavioralCase{
			{Request: spec.RequestShape{Method: "GET", Path: "/time"}, Expect: spec.ExpectShape{Status: 200, ContentType: "application/json", Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyPresent, Key: "zone"}}}},
		},
	}
	oc, ctx := ratifiedWithRequirement(t, req)
	res, err := BuildAndProve(ctx, oc.Store(), nil, nil, nil, "")
	if err != nil {
		t.Fatalf("build (must run, not error): %v", err)
	}
	if res.Verdict == "Accept" || res.Closed {
		t.Fatalf("a requirement the fixture can't satisfy must NOT be Accepted/closed: %+v", res)
	}
	if res.Delivery != "escalate" {
		t.Fatalf("delivery = %q, want escalate", res.Delivery)
	}
}
