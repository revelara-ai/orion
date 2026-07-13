package conductor

import (
	"strings"
	"sync"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// TestAmendmentRebuildReusesUntouchedVerdicts (or-7et.2 slice 2 Done-when,
// end-to-end): build a plan fully proven, amend ONE requirement (a compatible
// extra case the fixture already satisfies), and rebuild — only the covering
// task(s) re-prove; every untouched task reuses its persisted verdict.
func TestAmendmentRebuildReusesUntouchedVerdicts(t *testing.T) {
	if testing.Short() {
		t.Skip("two full build+prove passes; skipped in -short")
	}
	oc, ctx := ratifiedTimeService(t)

	if _, err := BuildAndProve(ctx, oc.Store(), nil, nil, nil, ""); err != nil {
		t.Fatalf("build 1: %v", err)
	}

	// Amend: one additional COMPATIBLE case on the same route (the fixture
	// already returns a "time" key, so proof still passes).
	if _, err := oc.AmendSpec(ctx); err != nil {
		t.Fatalf("amend: %v", err)
	}
	if err := oc.AddRequirement(ctx, spec.Requirement{
		Source: completeness.DimFunctional,
		Text:   `GET /time also always carries the "time" key`,
		Cases: []spec.BehavioralCase{{
			Request: spec.RequestShape{Method: "GET", Path: "/time"},
			Expect: spec.ExpectShape{Status: 200, ContentType: "application/json",
				Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyPresent, Key: "time"}}},
		}},
	}); err != nil {
		t.Fatalf("add case: %v", err)
	}
	if _, err := oc.ApproveSpec(ctx); err != nil { // auto-reconciles (or-7et.2)
		t.Fatalf("re-ratify: %v", err)
	}

	var mu sync.Mutex
	reused, fresh := 0, 0
	sink := PhaseSink(func(e PhaseEvent) {
		if e.Phase != "Prove" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case e.Status == PhaseDone && strings.Contains(e.Detail, "reused"):
			reused++
		case e.Status == PhaseRunning && strings.Contains(e.Detail, "behavioral"):
			fresh++
		}
	})
	if _, err := BuildAndProve(ctx, oc.Store(), nil, nil, sink, ""); err != nil {
		t.Fatalf("build 2 (post-amendment): %v", err)
	}
	if fresh == 0 {
		t.Fatal("the amended surface's covering task must be FRESHLY proven")
	}
	if reused == 0 {
		t.Fatal("untouched tasks must REUSE their persisted verdicts across the amendment")
	}
}
