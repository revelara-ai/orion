package harness

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/architect"
)

func TestRunIsDeterministic(t *testing.T) {
	model := sampleModel()
	h, err := Synthesize(SynthesizeOptions{RunID: "x", Model: model, Seed: 99})
	if err != nil {
		t.Fatal(err)
	}
	r := InProcessRunner{}
	m1, _ := r.Run(context.Background(), h, 7)
	m2, _ := r.Run(context.Background(), h, 7)
	if m1.LatencyP99Ms != m2.LatencyP99Ms || m1.ErrorRate != m2.ErrorRate || m1.PeakMemoryBytes != m2.PeakMemoryBytes || m1.TrialSeed != m2.TrialSeed {
		t.Errorf("non-deterministic Metrics:\n%+v\nvs\n%+v", m1, m2)
	}
}

func TestRunDifferentSaltsDiffer(t *testing.T) {
	h, err := Synthesize(SynthesizeOptions{RunID: "x", Model: sampleModel(), Seed: 99})
	if err != nil {
		t.Fatal(err)
	}
	r := InProcessRunner{}
	a, _ := r.Run(context.Background(), h, 1)
	b, _ := r.Run(context.Background(), h, 2)
	if a.PeakMemoryBytes == b.PeakMemoryBytes {
		t.Errorf("expected memory to differ across salts (jitter); both %d", a.PeakMemoryBytes)
	}
}

func TestPatchedDeltaImprovesMetrics(t *testing.T) {
	h, err := Synthesize(SynthesizeOptions{RunID: "x", Model: sampleModel(), Seed: 99})
	if err != nil {
		t.Fatal(err)
	}
	base, _ := InProcessRunner{}.Run(context.Background(), h, 1)
	patched, _ := InProcessRunner{PatchedDelta: 0.5}.Run(context.Background(), h, 1)
	if patched.LatencyP99Ms >= base.LatencyP99Ms {
		t.Errorf("patched should improve p99: base=%d patched=%d", base.LatencyP99Ms, patched.LatencyP99Ms)
	}
	if patched.ErrorRate >= base.ErrorRate {
		t.Errorf("patched should improve error rate: base=%g patched=%g", base.ErrorRate, patched.ErrorRate)
	}
}

func TestRunRejectsNilHarness(t *testing.T) {
	if _, err := (InProcessRunner{}).Run(context.Background(), nil, 0); err == nil {
		t.Error("expected error for nil harness")
	}
}

func TestRunReturnsAllAxes(t *testing.T) {
	h, _ := Synthesize(SynthesizeOptions{RunID: "x", Model: &architect.ArchitecturalModel{}, Seed: 1})
	m, err := InProcessRunner{}.Run(context.Background(), h, 1)
	if err != nil {
		t.Fatal(err)
	}
	if m.LatencyP99Ms == 0 {
		t.Error("LatencyP99Ms zero")
	}
	if m.PeakMemoryBytes == 0 {
		t.Error("PeakMemoryBytes zero")
	}
	if m.TrialSeed == 0 {
		t.Error("TrialSeed zero")
	}
}

func TestRunHonorsContextCancellation(t *testing.T) {
	h, _ := Synthesize(SynthesizeOptions{RunID: "x", Model: sampleModel(), Seed: 1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (InProcessRunner{}).Run(ctx, h, 1); err == nil {
		t.Error("expected ctx error")
	}
}
