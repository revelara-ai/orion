package formal

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/reliabilitytier"
)

// TestTriggerFiresOnCriticalTier (or-56c.4): critical tier always model-checks.
func TestTriggerFiresOnCriticalTier(t *testing.T) {
	d := ShouldCheck(TriggerInput{Tier: reliabilitytier.Critical, DesignTexts: []string{"a plain CRUD endpoint"}})
	if !d.Fire || !strings.Contains(d.Reason, "critical") {
		t.Fatalf("critical tier must fire, got %+v", d)
	}
}

// TestTriggerSkipsStatelessCRUDAtStandard (or-56c.4 acceptance): the manifesto
// calibration tenet — model-checking a stateless CRUD slice is waste.
func TestTriggerSkipsStatelessCRUDAtStandard(t *testing.T) {
	d := ShouldCheck(TriggerInput{
		Tier:        reliabilitytier.Standard,
		DesignTexts: []string{"GET /time returns the current time as JSON", "an HTTP endpoint that lists items"},
		Controllers: 1, ControlActions: 1,
	})
	if d.Fire {
		t.Fatalf("stateless CRUD at standard must skip, got %+v", d)
	}
	if !strings.Contains(d.Reason, "stateless") {
		t.Fatalf("the skip must be explained, got %q", d.Reason)
	}
}

// TestTriggerFiresOnConcurrencyShape: concurrency/ordering/shared-state/
// protocol vocabulary in the DESIGN fires the gate, and the reason names what
// matched (auditable, never a black box).
func TestTriggerFiresOnConcurrencyShape(t *testing.T) {
	for _, text := range []string{
		"workers consume the queue concurrently",
		"a state machine drives the payment protocol",
		"shared state between sessions must stay consistent",
		"acquire the lock before updating the ledger",
		"leader election among replicas",
	} {
		d := ShouldCheck(TriggerInput{Tier: reliabilitytier.Standard, DesignTexts: []string{text}})
		if !d.Fire {
			t.Fatalf("shape %q must fire the gate", text)
		}
		if d.Reason == "" {
			t.Fatal("the fire reason must name the matched shape")
		}
	}
}

// TestTriggerFiresOnControlStructureComplexity: a multi-controller STPA
// structure is ordering/coordination by construction.
func TestTriggerFiresOnControlStructureComplexity(t *testing.T) {
	d := ShouldCheck(TriggerInput{Tier: reliabilitytier.Standard, DesignTexts: []string{"simple api"}, Controllers: 3, ControlActions: 5})
	if !d.Fire || !strings.Contains(d.Reason, "control structure") {
		t.Fatalf("multi-controller designs must fire, got %+v", d)
	}
}

// TestTriggerSkipsThrowaway: calibration cuts both ways — never model-check a
// throwaway.
func TestTriggerSkipsThrowaway(t *testing.T) {
	d := ShouldCheck(TriggerInput{Tier: reliabilitytier.Throwaway, DesignTexts: []string{"concurrent workers race on a queue"}})
	if d.Fire {
		t.Fatalf("throwaway tier must skip regardless of shape, got %+v", d)
	}
}

// TestZeroInvariantModelIsNeverASilentPass (or-56c.4): a fired gate whose
// model declares NO invariants proves nothing — Inconclusive, loud.
func TestZeroInvariantModelIsNeverASilentPass(t *testing.T) {
	p := writeModel(t, `
action Init:
    x = 0

atomic action Done:
    require x == 0
    pass
`)
	_, err := CompileObligations(p)
	if err == nil || !strings.Contains(err.Error(), "no invariants") {
		t.Fatalf("a model with zero assertions must be Inconclusive, never a silent pass; got %v", err)
	}
}
