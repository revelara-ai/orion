package verify

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/architect"
	"github.com/revelara-ai/orion/internal/harness"
)

func makeHarness(t *testing.T) *harness.Harness {
	t.Helper()
	model := &architect.ArchitecturalModel{
		Services: []architect.Service{
			{Name: "frontend", Endpoints: []architect.Endpoint{
				{Kind: "http", Method: "GET /", SourceProvenance: "structural"},
			}, DownstreamDeps: []architect.DownstreamDep{
				{TargetName: "cart", Protocol: "grpc"},
			}},
		},
	}
	h, err := harness.Synthesize(harness.SynthesizeOptions{
		RunID: "verify-test", Model: model, Seed: 42,
		Thoroughness: harness.ThoroughnessFast,
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	return h
}

// uniformImproverRunner returns metrics that uniformly improve over a
// baseline by `improvement`. Used to test the "all axes decisive"
// path that harness.InProcessRunner can't satisfy on its own (memory
// in InProcessRunner is jitter-only, not delta-driven).
type uniformImproverRunner struct {
	baselineLatencyMs   int
	baselineErrorRate   float64
	baselineCascadeProb float64
	baselineMemoryBytes int64
	improvement         float64
}

func (r uniformImproverRunner) Run(_ context.Context, h *harness.Harness, salt int64) (*harness.Metrics, error) {
	factor := 1.0 - r.improvement
	if factor < 0 {
		factor = 0
	}
	return &harness.Metrics{
		LatencyP99Ms:       int(float64(r.baselineLatencyMs) * factor),
		ErrorRate:          r.baselineErrorRate * factor,
		CascadeProbability: r.baselineCascadeProb * factor,
		PeakMemoryBytes:    int64(float64(r.baselineMemoryBytes) * factor),
		TrialSeed:          salt,
	}, nil
}

func TestLoopAcceptsClearImprovement(t *testing.T) {
	h := makeHarness(t)
	cfg := DefaultLoopConfig()
	cfg.MaxTrials = 12
	cfg.MinTrials = 8
	baseline := uniformImproverRunner{
		baselineLatencyMs: 200, baselineErrorRate: 0.05,
		baselineCascadeProb: 0.5, baselineMemoryBytes: 100000,
		improvement: 0,
	}
	patched := baseline
	patched.improvement = 0.5
	v, err := Loop(context.Background(), h, baseline, patched, cfg)
	if err != nil {
		t.Fatalf("Loop: %v", err)
	}
	if v.Decision != DecisionAccepted {
		t.Errorf("Decision = %q, want %q\n%+v", v.Decision, DecisionAccepted, v.Axes)
	}
	if v.PairedTrialsConsumed < cfg.MinTrials {
		t.Errorf("PairedTrialsConsumed = %d, want ≥ MinTrials=%d", v.PairedTrialsConsumed, cfg.MinTrials)
	}
	if len(v.Axes) != 4 {
		t.Errorf("expected 4 axes, got %d", len(v.Axes))
	}
}

func TestLoopRejectsRegression(t *testing.T) {
	h := makeHarness(t)
	cfg := DefaultLoopConfig()
	cfg.MaxTrials = 12
	cfg.MinTrials = 8
	// Patched is WORSE than baseline (negative delta means worse).
	v, err := Loop(context.Background(), h,
		harness.InProcessRunner{PatchedDelta: 0.5}, // baseline (which is "improved" - swap roles)
		harness.InProcessRunner{},                  // patched
		cfg,
	)
	if err != nil {
		t.Fatalf("Loop: %v", err)
	}
	// Either RejectedRegression or RejectedNoDominance is acceptable —
	// both indicate the patch is not better. The key invariant is
	// "not Accepted".
	if v.Decision == DecisionAccepted {
		t.Errorf("Decision = Accepted but patched is worse than baseline\n%+v", v.Axes)
	}
}

func TestLoopRejectsNoDominanceWhenIdentical(t *testing.T) {
	h := makeHarness(t)
	cfg := DefaultLoopConfig()
	cfg.MaxTrials = 12
	cfg.MinTrials = 8
	// Identical runners: deltas should center on zero, no axis should
	// favor patched. SPEC §12.6 says this should be rejected_no_dominance
	// after MinTrials.
	v, err := Loop(context.Background(), h,
		harness.InProcessRunner{},
		harness.InProcessRunner{},
		cfg,
	)
	if err != nil {
		t.Fatalf("Loop: %v", err)
	}
	if v.Decision == DecisionAccepted {
		t.Errorf("identical runners should not yield Accepted: %+v", v)
	}
}

func TestLoopRecordsHarnessSeed(t *testing.T) {
	h := makeHarness(t)
	v, err := Loop(context.Background(), h,
		harness.InProcessRunner{},
		harness.InProcessRunner{PatchedDelta: 0.5},
		DefaultLoopConfig(),
	)
	if err != nil {
		t.Fatalf("Loop: %v", err)
	}
	if v.HarnessSeed != h.Seed {
		t.Errorf("HarnessSeed = %d, want %d", v.HarnessSeed, h.Seed)
	}
}

func TestLoopConfigForThoroughnessSetsCap(t *testing.T) {
	cases := map[harness.Thoroughness]int{
		harness.ThoroughnessFast:     12,
		harness.ThoroughnessStandard: 24,
		harness.ThoroughnessThorough: 48,
	}
	for thor, want := range cases {
		got := LoopConfigForThoroughness(thor).MaxTrials
		if got != want {
			t.Errorf("MaxTrials for %q = %d, want %d", thor, got, want)
		}
	}
}

func TestLoopHonorsContextCancellation(t *testing.T) {
	h := makeHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Loop(ctx, h,
		harness.InProcessRunner{},
		harness.InProcessRunner{PatchedDelta: 0.5},
		DefaultLoopConfig(),
	); err == nil {
		t.Error("expected context error")
	}
}
