package conductor

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// TestRenderPhaseReport: the report shows each phase once (last status), in order,
// with the right glyph.
func TestRenderPhaseReport(t *testing.T) {
	events := []PhaseEvent{
		{Phase: "Decompose", Status: PhaseRunning},
		{Phase: "Decompose", Status: PhaseDone, Detail: "1 task"},
		{Phase: "Generate", Status: PhaseRunning},
		{Phase: "Generate", Status: PhaseDone},
		{Phase: "Prove", Status: PhaseDone, Detail: "Accept"},
		{Phase: "Align", Status: PhaseWarn, Detail: "returns a constant"},
	}
	got := RenderPhaseReport(events)
	want := "✓ Decompose — 1 task\n✓ Generate\n✓ Prove — Accept\n⚠ Align — returns a constant"
	if got != want {
		t.Fatalf("report =\n%q\nwant\n%q", got, want)
	}
}

// TestBuildEmitsPhaseSequence: a real build emits the expected ordered phase
// sequence with terminal statuses (Step 0 — the pipeline is now legible).
func TestBuildEmitsPhaseSequence(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a service + proof")
	}
	oc, ctx := ratifiedTimeService(t)

	var phases []PhaseEvent
	// inject a misaligned aligner so the Align phase emits a warn glyph
	aligner := func(context.Context, string, string, []spec.BehavioralCase) (AlignVerdict, error) {
		return AlignVerdict{Aligned: false, Severity: "high", Concern: "x"}, nil
	}
	res, err := BuildAndProve(ctx, oc.Store(), nil, aligner, func(e PhaseEvent) { phases = append(phases, e) }, "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	report := RenderPhaseReport(phases)
	for _, want := range []string{"Decompose", "Generate", "Prove", "Align", "Deliver"} {
		if !strings.Contains(report, want) {
			t.Errorf("phase report missing %q:\n%s", want, report)
		}
	}
	// canonical build proves green; the misaligned aligner is log-only (warn, not block)
	if res.Verdict != "Accept" {
		t.Fatalf("verdict = %s, want Accept", res.Verdict)
	}
	if !strings.Contains(report, "✓ Prove — Accept") {
		t.Errorf("prove phase not green:\n%s", report)
	}
	if !strings.Contains(report, "⚠ Align") {
		t.Errorf("align phase should warn (log-only):\n%s", report)
	}
}
