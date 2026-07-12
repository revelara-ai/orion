package decomposer

import (
	"strings"
	"testing"
)

func goodOutcome() ShadowOutcome {
	return ShadowOutcome{SupersetOK: true, FloorOK: true, CoverageGateOK: true, ProposerClusters: 3, OracleClusters: 3}
}

func TestCutoverReadyRequiresFullWindow(t *testing.T) {
	outs := make([]ShadowOutcome, 10)
	for i := range outs {
		outs[i] = goodOutcome()
	}
	ready, reason := CutoverReady(outs, 50)
	if ready {
		t.Fatal("10 runs must not satisfy a 50-run window")
	}
	if !strings.Contains(reason, "10/50") {
		t.Fatalf("reason must show window progress, got %q", reason)
	}
}

func TestCutoverReadyAllGreenWindow(t *testing.T) {
	outs := make([]ShadowOutcome, 50)
	for i := range outs {
		outs[i] = goodOutcome()
	}
	if ready, reason := CutoverReady(outs, 50); !ready {
		t.Fatalf("an all-green full window must be ready, got %q", reason)
	}
}

func TestCutoverReadyRejectsClusterCollapse(t *testing.T) {
	// The judge-panel hazard (or-809): a FileScope collision collapses the DAG
	// to one cluster — coverage stays green but parallelism regresses.
	outs := make([]ShadowOutcome, 50)
	for i := range outs {
		outs[i] = goodOutcome()
	}
	outs[7].ProposerClusters, outs[7].OracleClusters = 1, 4
	ready, reason := CutoverReady(outs, 50)
	if ready {
		t.Fatal("a cluster-count regression inside the window must block cutover")
	}
	if !strings.Contains(reason, "cluster") {
		t.Fatalf("reason must name the regression, got %q", reason)
	}
}

func TestCutoverReadyRejectsAnyGateFailure(t *testing.T) {
	for _, mutate := range []func(*ShadowOutcome){
		func(o *ShadowOutcome) { o.SupersetOK = false },
		func(o *ShadowOutcome) { o.FloorOK = false },
		func(o *ShadowOutcome) { o.CoverageGateOK = false },
	} {
		outs := make([]ShadowOutcome, 50)
		for i := range outs {
			outs[i] = goodOutcome()
		}
		mutate(&outs[49])
		if ready, _ := CutoverReady(outs, 50); ready {
			t.Fatal("any gate failure inside the window must block cutover")
		}
	}
}
