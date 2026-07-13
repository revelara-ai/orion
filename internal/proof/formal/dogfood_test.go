package formal

import (
	"context"
	"os"
	"testing"
)

// controlPlaneModels is the or-56c.5 dogfood: Orion's OWN control-plane
// designs, model-checked on every full test lane. Each model rolls out
// advisory→blocking independently (mirroring the AlignmentGate): an advisory
// failure LOGS (an immature checker or model must not wedge Orion's own CI);
// a blocking failure fails the lane. ORION_DESIGN_PROOF=block forces all
// blocking; flip `blocking: true` per model once its window is stable.
var controlPlaneModels = []struct {
	name     string
	path     string
	blocking bool // stable since or-56c.1 → blocking; fresh models start advisory
}{
	{"integration-queue S1/S2 (or-1lz leases)", "testdata/integration_queue_guarded.fizz", true},
	{"done-gate soundness (proven only through Accept)", "testdata/done_gate_guarded.fizz", false},
	{"DAG scheduler liveness (no wedged interleaving)", "testdata/dag_liveness.fizz", false},
}

// TestControlPlaneDesignProofs (or-56c.5 acceptance): the control-plane models
// run as a CI gate on Orion itself.
func TestControlPlaneDesignProofs(t *testing.T) {
	if testing.Short() {
		t.Skip("model checking execs the fizzbee toolchain")
	}
	fb := requireFizzBee(t)
	forceBlock := os.Getenv("ORION_DESIGN_PROOF") == "block"
	for _, m := range controlPlaneModels {
		t.Run(m.name, func(t *testing.T) {
			// A blocking model runs the FULL refinement chain (or-56c.3): verified
			// invariants must be bound to resolving, passing behavioral tests.
			// Advisory models are Check-only until they flip (bindings come with
			// the flip — an unbound advisory model must not fail the lane).
			if m.blocking {
				if err := EnforceRefinement(context.Background(), repoRoot(t), m.path, fb); err != nil {
					t.Fatal(err)
				}
				return
			}
			r, err := fb.Check(context.Background(), m.path)
			if err != nil {
				t.Fatalf("checker error: %v", err)
			}
			if r.Skipped != "" {
				t.Skip(r.Skipped)
			}
			if r.Passed {
				return
			}
			msg := "design proof FAILED: invariant=" + r.Invariant
			if r.Deadlock {
				msg = "design proof FAILED: DEADLOCK (liveness)"
			}
			if m.blocking || forceBlock {
				t.Fatalf("%s\n%s", msg, r.Output)
			}
			t.Logf("ADVISORY %s (flips to blocking once stable)\n%s", msg, r.Output)
		})
	}
}

// TestCheckerCatchesDoneGateBypass: the checker is not vacuous — deleting the
// Accept trigger must violate ProvenOnlyThroughAccept.
func TestCheckerCatchesDoneGateBypass(t *testing.T) {
	if testing.Short() {
		t.Skip("model checking execs the fizzbee toolchain")
	}
	r, err := requireFizzBee(t).Check(context.Background(), "testdata/done_gate_unguarded.fizz")
	if err != nil {
		t.Fatal(err)
	}
	if r.Skipped != "" {
		t.Skip(r.Skipped)
	}
	if r.Passed || r.Invariant != "ProvenOnlyThroughAccept" {
		t.Fatalf("trigger-less done-gate must violate ProvenOnlyThroughAccept, got passed=%v invariant=%q", r.Passed, r.Invariant)
	}
}

// TestCheckerCatchesWedgedScheduler: a Complete that never releases its lease
// must deadlock the scheduler model — the liveness failure class.
func TestCheckerCatchesWedgedScheduler(t *testing.T) {
	if testing.Short() {
		t.Skip("model checking execs the fizzbee toolchain")
	}
	r, err := requireFizzBee(t).Check(context.Background(), "testdata/dag_liveness_wedged.fizz")
	if err != nil {
		t.Fatal(err)
	}
	if r.Skipped != "" {
		t.Skip(r.Skipped)
	}
	if r.Passed || !r.Deadlock {
		t.Fatalf("lease-never-released must DEADLOCK, got passed=%v deadlock=%v\n%s", r.Passed, r.Deadlock, firstLine(r.Output))
	}
}
