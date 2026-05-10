package harness

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/architect"
)

func sampleModel() *architect.ArchitecturalModel {
	return &architect.ArchitecturalModel{
		Repo: "/repo",
		Services: []architect.Service{
			{
				Name:     "frontend",
				Language: "go",
				Endpoints: []architect.Endpoint{
					{Kind: "http", Method: "GET /", SourceProvenance: "structural"},
					{Kind: "grpc", Service: "Cart", Method: "AddItem", SourceProvenance: "structural"},
				},
				DownstreamDeps: []architect.DownstreamDep{
					{TargetName: "cart", Kind: "service", Protocol: "grpc"},
					{TargetName: "checkout", Kind: "service", Protocol: "grpc"},
				},
			},
			{
				Name: "checkout",
				DownstreamDeps: []architect.DownstreamDep{
					{TargetName: "payment", Kind: "service", Protocol: "grpc"},
					{TargetName: "cart", Kind: "service", Protocol: "grpc"}, // dup, must dedup
				},
			},
		},
	}
}

func TestSynthesizeIsDeterministic(t *testing.T) {
	model := sampleModel()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	opts := SynthesizeOptions{
		RunID:        "abc123",
		Model:        model,
		Seed:         42,
		Thoroughness: ThoroughnessStandard,
		Now:          now,
	}
	h1, err := Synthesize(opts)
	if err != nil {
		t.Fatalf("Synthesize 1: %v", err)
	}
	h2, err := Synthesize(opts)
	if err != nil {
		t.Fatalf("Synthesize 2: %v", err)
	}
	b1, _ := json.Marshal(h1)
	b2, _ := json.Marshal(h2)
	if string(b1) != string(b2) {
		t.Errorf("non-deterministic JSON\n%s\nvs\n%s", b1, b2)
	}
}

func TestSynthesizeDifferentSeedsDiffer(t *testing.T) {
	model := sampleModel()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	a, _ := Synthesize(SynthesizeOptions{RunID: "x", Model: model, Seed: 1, Now: now})
	b, _ := Synthesize(SynthesizeOptions{RunID: "x", Model: model, Seed: 2, Now: now})
	if a == nil || b == nil {
		t.Fatal("nil harness")
	}
	if a.Workload.Endpoints[0].PayloadBytes == b.Workload.Endpoints[0].PayloadBytes {
		t.Errorf("expected payload to differ across seeds; both %d", a.Workload.Endpoints[0].PayloadBytes)
	}
}

func TestNamespaceCanonical(t *testing.T) {
	cases := []struct{ runID, want string }{
		{"abc123", "orion-run-abc123"},
		{"ABC123-very-long-id", "orion-run-abc123"},
		{"!!!", "orion-run-anon"},
		{"", "orion-run-anon"},
	}
	for _, c := range cases {
		got := canonicalNamespace(c.runID)
		if got != c.want {
			t.Errorf("canonicalNamespace(%q) = %q, want %q", c.runID, got, c.want)
		}
	}
}

func TestSynthesizeRejectsBadInputs(t *testing.T) {
	cases := []SynthesizeOptions{
		{},
		{RunID: "x"},
		{Model: sampleModel()},
		{RunID: "x", Model: sampleModel(), Thoroughness: "weird"},
	}
	for i, c := range cases {
		if _, err := Synthesize(c); !errors.Is(err, ErrInvalidInputs) {
			t.Errorf("case %d: expected ErrInvalidInputs, got %v", i, err)
		}
	}
}

func TestWorkloadCoversAllEndpoints(t *testing.T) {
	model := sampleModel()
	h, err := Synthesize(SynthesizeOptions{RunID: "x", Model: model, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"frontend|GET /":        false,
		"frontend|Cart/AddItem": false,
	}
	for _, e := range h.Workload.Endpoints {
		key := e.Service + "|" + e.Endpoint
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for k, hit := range want {
		if !hit {
			t.Errorf("workload missing endpoint %q", k)
		}
	}
}

func TestFaultsCoverAllUniqueDeps(t *testing.T) {
	model := sampleModel()
	h, err := Synthesize(SynthesizeOptions{RunID: "x", Model: model, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"cart": false, "checkout": false, "payment": false}
	for _, f := range h.Faults.Faults {
		want[f.TargetName] = true
	}
	for k, hit := range want {
		if !hit {
			t.Errorf("faults missing dep %q", k)
		}
	}
	if len(h.Faults.Faults) != 3 {
		t.Errorf("expected 3 unique deps, got %d", len(h.Faults.Faults))
	}
}

func TestThoroughnessScalesFaults(t *testing.T) {
	model := sampleModel()
	fast, _ := Synthesize(SynthesizeOptions{RunID: "x", Model: model, Seed: 7, Thoroughness: ThoroughnessFast})
	thorough, _ := Synthesize(SynthesizeOptions{RunID: "x", Model: model, Seed: 7, Thoroughness: ThoroughnessThorough})
	if fast.Faults.Faults[0].LatencyP50Ms >= thorough.Faults.Faults[0].LatencyP50Ms {
		t.Errorf("expected thorough latency > fast latency: fast=%d thorough=%d",
			fast.Faults.Faults[0].LatencyP50Ms, thorough.Faults.Faults[0].LatencyP50Ms)
	}
	if fast.Workload.TargetRPS >= thorough.Workload.TargetRPS {
		t.Errorf("expected thorough RPS > fast RPS")
	}
}

func TestSynthesizeFallbackForEmptyModel(t *testing.T) {
	h, err := Synthesize(SynthesizeOptions{RunID: "x", Model: &architect.ArchitecturalModel{}, Seed: 1})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(h.Workload.Endpoints) == 0 {
		t.Error("expected synthetic fallback endpoint")
	}
	if len(h.Faults.Faults) == 0 {
		t.Error("expected synthetic fallback fault")
	}
}
