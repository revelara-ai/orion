package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// TestApproveSpecRejectsZeroCases: the or-y9d false-pass class — an intent that
// never lowers into any behavioral case must NOT ratify. Nothing to execute means
// nothing can be proven, and "proven" is the only right to ship.
func TestApproveSpecRejectsZeroCases(t *testing.T) {
	store, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c := NewWithStore(store)
	ctx := context.Background()

	// A CLI intent compiles to a minimal non-HTTP contract: no synthesized default
	// case, and no requirement was ever declared → zero cases.
	if _, err := c.Submit(ctx, "a CLI tool that prints the date"); err != nil {
		t.Fatal(err)
	}
	_, err = c.ApproveSpec(ctx)
	if err == nil {
		t.Fatal("a spec with zero behavioral cases must not ratify (vacuous proof)")
	}
	if !strings.Contains(err.Error(), "behavioral case") {
		t.Errorf("rejection should tell the agent what is missing, got: %v", err)
	}

	// Declaring the behavior unblocks ratification.
	if err := c.AddRequirement(ctx, spec.Requirement{
		Source: completeness.DimFunctional,
		Text:   "the date verify command exits zero",
		Cases: []spec.BehavioralCase{{
			Request: spec.RequestShape{Method: "GET", Path: "/healthz"},
			Expect: spec.ExpectShape{Status: 200, ContentType: "application/json",
				Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyPresent, Key: "date"}}},
		}},
	}); err != nil {
		t.Fatalf("declare requirement: %v", err)
	}
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatalf("approve assumptions: %v", err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatalf("with >=1 case the spec must ratify: %v", err)
	}
}
