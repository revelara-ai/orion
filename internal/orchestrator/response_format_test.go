package orchestrator

import (
	"strings"
	"testing"
)

// TestRecordedJSONFormatReachesContract reproduces the dogfooding defect: a
// developer (or the Orion agent) records response_format as a JSON *variant*
// ("application/json"), and the assembled spec's machine-readable contract must
// say application/json — not silently degrade to text/plain.
func TestRecordedJSONFormatReachesContract(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatalf("submit: %v", err)
	}
	for _, a := range []struct{ k, v string }{
		{"response_format", "application/json"}, // the value an LLM naturally records
		{"timezone", "UTC"}, {"port", "8080"}, {"route", "/time"},
	} {
		if err := c.RecordAnswer(ctx, a.k, a.v); err != nil {
			t.Fatalf("answer %s: %v", a.k, err)
		}
	}
	es, err := c.PreviewSpec(ctx)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if es.ResponseContract.ContentType != "application/json" {
		t.Fatalf("content_type = %q, want application/json (recorded value must reach the contract)", es.ResponseContract.ContentType)
	}
}

// TestUnrecognizedFormatFailsLoud: an unrecognized format makes assembly error
// rather than anchoring a contradictory text/plain contract — the invariant the
// Orion agent was defending when it refused to ratify.
func TestUnrecognizedFormatFailsLoud(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatalf("submit: %v", err)
	}
	for _, a := range []struct{ k, v string }{
		{"response_format", "protobuf"}, {"timezone", "UTC"}, {"port", "8080"}, {"route", "/time"},
	} {
		if err := c.RecordAnswer(ctx, a.k, a.v); err != nil {
			t.Fatalf("answer %s: %v", a.k, err)
		}
	}
	_, err := c.PreviewSpec(ctx)
	if err == nil {
		t.Fatal("preview must fail loud on an unrecognized format, not assemble a text/plain contract")
	}
	if !strings.Contains(err.Error(), "not a recognized format") {
		t.Fatalf("error should explain the format problem, got: %v", err)
	}
}
