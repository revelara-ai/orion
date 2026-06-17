package report

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/harness"
	"github.com/revelara-ai/orion/internal/stats"
	"github.com/revelara-ai/orion/internal/verify"
)

func TestRenderEmptyAccepted(t *testing.T) {
	out := Render(Data{
		RunID: "run123",
		Issue: Issue{Title: "Wire timeouts everywhere", ExternalID: "gh-svc-123"},
	})
	for _, want := range []string{
		"## Orion run run123",
		"Source issue:** gh-svc-123 — Wire timeouts everywhere",
		"### No improvements",
		"Operating envelope",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRenderWithHarness(t *testing.T) {
	h := &harness.Harness{
		RunID: "run123", Seed: 42, Thoroughness: harness.ThoroughnessStandard,
		Namespace: "orion-run-run123",
		Workload: harness.WorkloadConfig{
			Endpoints: []harness.EndpointDist{{Service: "f", Endpoint: "/", Weight: 1, PayloadBytes: 1}},
			TargetRPS: 100, DurationSeconds: 30,
		},
		Faults: harness.FaultConfig{Faults: []harness.FaultProfile{{TargetName: "x"}}},
	}
	out := Render(Data{RunID: "run123", Harness: h})
	for _, want := range []string{
		"### Harness configuration",
		"Namespace: `orion-run-run123`",
		"Seed: `42`",
		"Thoroughness: `standard`",
		"Workload: 1 endpoints, target 100 RPS, 30 s per trial",
		"Faults: 1 profiled dependencies",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n%s", want, out)
		}
	}
}

func TestRenderAcceptedTable(t *testing.T) {
	v := verify.Verdict{
		Decision:             verify.DecisionAccepted,
		PairedTrialsConsumed: 12,
		MaxTrials:            12,
		Axes: []verify.AxisMetrics{
			{
				Axis: verify.AxisLatencyP99, BaselineMean: 200, PatchedMean: 100,
				DeltaCI:    stats.ConfidenceInterval{Lower: 80, Upper: 120, Level: 0.95},
				TrialCount: 12, Decision: stats.AxisDecisive,
			},
		},
	}
	out := Render(Data{
		RunID: "r1",
		AcceptedPatches: []AcceptedPatch{
			{
				GapID: "g001", ControlID: "RC-T-1", Pattern: "timeout",
				TargetPath: "client.go", LLMModel: "test", LLMSeed: 7,
				Verdict: v,
			},
		},
		BundleURLPlaceholder: "orion-runs/r1/bundle.json",
	})
	for _, want := range []string{
		"#### 1. timeout [`RC-T-1`] — `client.go`",
		"Gap: `g001`",
		"LLM: `test` seed=7",
		"Trials consumed: 12 / 12",
		"`latency_p99_ms`",
		"`decisive`",
		"Bundle: `orion-runs/r1/bundle.json`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n%s", want, out)
		}
	}
}

func TestFormatFloat(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"}, {1, "1"}, {1.5, "1.5"}, {0.1234, "0.1234"},
	}
	for _, c := range cases {
		if got := formatFloat(c.in); got != c.want {
			t.Errorf("formatFloat(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
