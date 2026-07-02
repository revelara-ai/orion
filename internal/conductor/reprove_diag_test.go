package conductor

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// TestEpicReproverSurfacesRedDiagnostics (or-v9f.21): a red re-proof of the
// ASSEMBLED tree must tell the operator why — the causal analysis reaches the
// phase stream instead of vanishing into the integrator's rollback.
func TestEpicReproverSurfacesRedDiagnostics(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + proves an artifact; skipped in -short")
	}
	store := openStore(t)
	gs := sandbox.GenSpec{Module: "orion-generated/svc", Route: "/time", Port: 8080, Format: "json", TimeZone: "UTC"}
	timeCase := spec.BehavioralCase{
		Request: spec.RequestShape{Method: "GET", Path: "/time"},
		Expect: spec.ExpectShape{Status: 200, ContentType: "application/json",
			Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyRFC3339, Key: "time"}}},
	}
	timeCase.EnsureID()
	contract := testsynth.Contract{Route: "/time", Format: "json", TimeZone: "UTC", EntrySymbol: gs.Entry(), Cases: []spec.BehavioralCase{timeCase}}
	es := spec.ExecutableSpec{ResponseContract: spec.ResponseContract{Route: "/time", Cases: []spec.BehavioralCase{timeCase}}}

	var mu sync.Mutex
	var events []string
	sink := PhaseSink(func(e PhaseEvent) {
		mu.Lock()
		events = append(events, e.Phase+"|"+string(e.Status)+"|"+e.Detail)
		mu.Unlock()
	})

	var captured bool
	reprove := epicReprover(store, contract, stpa.Model{}, es, nil, "", sink, func(proof.Report) { captured = true })

	// RED: the broken artifact fails the behavioral/empirical contract.
	dir := t.TempDir()
	if _, err := writeBrokenTimeService(dir, gs); err != nil {
		t.Fatal(err)
	}
	ok, err := reprove(context.Background(), dir)
	if err != nil {
		t.Fatalf("a red re-proof is a verdict, not an error: %v", err)
	}
	if ok {
		t.Fatal("a broken assembled tree must re-prove red")
	}
	if !captured {
		t.Fatal("the assembled report must be captured for the drift check")
	}
	mu.Lock()
	joined := strings.Join(events, "\n")
	mu.Unlock()
	if !strings.Contains(joined, "re-proof RED") {
		t.Fatalf("the red re-proof must surface in the phase stream, got:\n%s", joined)
	}
	red := ""
	for _, e := range events {
		if strings.Contains(e, "re-proof RED") {
			red = e
		}
	}
	if len(red) < len("Integrate|warn|assembled-tree re-proof RED — ")+10 {
		t.Fatalf("the event must carry a causal analysis, not just a verdict: %q", red)
	}

	// GREEN: a proven artifact emits no red diagnostics.
	mu.Lock()
	events = nil
	mu.Unlock()
	greenDir := t.TempDir()
	if _, err := sandbox.GenerateTimeServiceFixture(greenDir, gs); err != nil {
		t.Fatal(err)
	}
	ok, err = reprove(context.Background(), greenDir)
	if err != nil || !ok {
		t.Fatalf("the fixture must re-prove green: ok=%v err=%v", ok, err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, e := range events {
		if strings.Contains(e, "RED") {
			t.Fatalf("a green re-proof must not emit red diagnostics: %q", e)
		}
	}
}
