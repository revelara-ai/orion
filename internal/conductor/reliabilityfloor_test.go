package conductor

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/reliabilityfloor"
)

func TestFloorSignalsUsesSeam(t *testing.T) {
	orig := floorSource
	t.Cleanup(func() { floorSource = orig })
	floorSource = func(_ *contextstore.Store) reliabilityfloor.SignalSource {
		return &reliabilityfloor.FakeSource{Signals: []reliabilityfloor.Signal{
			{ID: "RC-1", Title: "Outbound HTTP without timeout", Severity: reliabilityfloor.SevHigh},
		}}
	}
	got := floorSignals(context.Background(), nil, "", "add an http client call")
	if len(got) != 1 || got[0].ID != "RC-1" {
		t.Fatalf("floorSignals=%v want RC-1", got)
	}
	if got[0].Check.Kind != reliabilityfloor.CheckGolangciLint {
		t.Fatalf("expected check attached, got %v", got[0].Check.Kind)
	}
}

func TestFloorSignalsNilStoreFailsOpen(t *testing.T) {
	// ChangeAndProve is exercised with a nil store in tests and tolerates it everywhere
	// else; the floor must fail open (no signals), never construct a Consumer that
	// panics in its fetch goroutines.
	if got := floorSignals(context.Background(), nil, "", "any intent"); got != nil {
		t.Fatalf("nil store must yield no signals, got %v", got)
	}
}

func TestRunFloorChecksSkipsWithoutMechanizableSignals(t *testing.T) {
	// Advisory-only signals produce nil lint args -> the runner must Skip, never error.
	r := runFloorChecks(context.Background(), t.TempDir(),
		[]reliabilityfloor.Signal{{ID: "K-1", Title: "Design for graceful degradation"}}, // no keyword match
		[]string{"internal/a/x.go"})
	if r.Ran || r.Skipped == "" {
		t.Fatalf("advisory-only signals must skip lint, got %+v", r)
	}
}
