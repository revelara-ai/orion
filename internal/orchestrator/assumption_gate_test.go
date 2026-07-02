package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// TestRatificationRequiresAssumptionApproval (or-v9f.19): fallback assumptions
// ride into a ratified spec ONLY over a recorded approval act — prompt
// discipline is not a gate.
func TestRatificationRequiresAssumptionApproval(t *testing.T) {
	store, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c := NewWithStore(store)
	ctx := context.Background()

	if _, err := c.Submit(ctx, "Build an HTTP service that returns the current time."); err != nil {
		t.Fatal(err)
	}
	for _, a := range [][2]string{{"response_format", "json"}, {"timezone", "UTC"}, {"port", "8080"}, {"route", "/time"}} {
		if err := c.RecordAnswer(ctx, a[0], a[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.AddRequirement(ctx, spec.Requirement{
		Source: completeness.DimFunctional,
		Text:   "GET /time returns an RFC3339 time key",
		Cases: []spec.BehavioralCase{{
			Request: spec.RequestShape{Method: "GET", Path: "/time"},
			Expect: spec.ExpectShape{Status: 200, ContentType: "application/json",
				Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyRFC3339, Key: "time"}}},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	_, err = c.ApproveSpec(ctx)
	if err == nil {
		t.Fatal("unapproved assumptions must refuse ratification")
	}
	if !strings.Contains(err.Error(), "approve_assumptions") || !strings.Contains(err.Error(), "assumption") {
		t.Errorf("the refusal must name the act that unblocks it, got: %v", err)
	}

	approved, err := c.ApproveAssumptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(approved) == 0 {
		t.Fatal("the time-service spec resolves non-functional dimensions via fallbacks; approval must record them")
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatalf("after recorded approval the spec must ratify: %v", err)
	}
}
