package orchestration

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/harness"
	"github.com/revelara-ai/orion/internal/patches"
	"github.com/revelara-ai/orion/internal/stats"
	"github.com/revelara-ai/orion/internal/verify"
)

func decisiveAxis(axis verify.Axis, deltaLower float64) verify.AxisMetrics {
	return verify.AxisMetrics{
		Axis: axis, BaselineMean: 200, PatchedMean: 100,
		DeltaCI:    stats.ConfidenceInterval{Lower: deltaLower, Upper: deltaLower + 20, Level: 0.95},
		TrialCount: 12, Decision: stats.AxisDecisive,
	}
}

func sampleHarness() *harness.Harness {
	return &harness.Harness{
		RunID: "r1", Seed: 1, Thoroughness: harness.ThoroughnessStandard,
		Namespace: "orion-run-r1",
	}
}

func TestComposeReturnsNilForEmpty(t *testing.T) {
	if Compose(RunOptions{RunID: "r1"}, sampleHarness(), nil) != nil {
		t.Error("expected nil PRPlan for empty accepted")
	}
}

func TestComposeOrdersByImprovement(t *testing.T) {
	a := VerifiedPatch{
		Patch:   patches.CandidatePatch{GapID: "g1", TargetPath: "a.go", ControlID: "RC-1"},
		Verdict: verify.Verdict{Axes: []verify.AxisMetrics{decisiveAxis(verify.AxisLatencyP99, 10)}},
	}
	b := VerifiedPatch{
		Patch:   patches.CandidatePatch{GapID: "g2", TargetPath: "b.go", ControlID: "RC-2"},
		Verdict: verify.Verdict{Axes: []verify.AxisMetrics{decisiveAxis(verify.AxisLatencyP99, 100)}},
	}
	plan := Compose(RunOptions{RunID: "r1", Issue: Issue{Title: "T", ExternalID: "x"}}, sampleHarness(), []VerifiedPatch{a, b})
	if plan == nil || len(plan.Commits) != 2 {
		t.Fatalf("got plan=%+v", plan)
	}
	// Larger improvement first.
	if plan.Commits[0].TargetPath != "b.go" {
		t.Errorf("expected b.go first, got %s", plan.Commits[0].TargetPath)
	}
	if plan.Commits[1].TargetPath != "a.go" {
		t.Errorf("expected a.go second, got %s", plan.Commits[1].TargetPath)
	}
}

func TestComposeDropsConflictingTargetPath(t *testing.T) {
	a := VerifiedPatch{
		Patch:   patches.CandidatePatch{GapID: "g1", TargetPath: "same.go", ControlID: "RC-1"},
		Verdict: verify.Verdict{Axes: []verify.AxisMetrics{decisiveAxis(verify.AxisLatencyP99, 100)}},
	}
	b := VerifiedPatch{
		Patch:   patches.CandidatePatch{GapID: "g2", TargetPath: "same.go", ControlID: "RC-2"},
		Verdict: verify.Verdict{Axes: []verify.AxisMetrics{decisiveAxis(verify.AxisLatencyP99, 50)}},
	}
	plan := Compose(RunOptions{RunID: "r1"}, sampleHarness(), []VerifiedPatch{a, b})
	if plan == nil || len(plan.Commits) != 1 {
		t.Fatalf("got %+v", plan)
	}
	if plan.Commits[0].Patch.GapID != "g1" {
		t.Errorf("expected larger-improvement g1 to win, got %s", plan.Commits[0].Patch.GapID)
	}
}

func TestComposeBranchAndTitle(t *testing.T) {
	vp := VerifiedPatch{
		Patch:   patches.CandidatePatch{GapID: "g1", TargetPath: "a.go", ControlID: "RC-T-1", Pattern: patches.PatternTimeout},
		Verdict: verify.Verdict{Axes: []verify.AxisMetrics{decisiveAxis(verify.AxisLatencyP99, 50)}},
	}
	plan := Compose(RunOptions{
		RunID: "abc123",
		Issue: Issue{Title: "Wire timeouts", ExternalID: "gh-svc-7"},
	}, sampleHarness(), []VerifiedPatch{vp})
	if !strings.HasPrefix(plan.BranchName, "orion/abc123-") {
		t.Errorf("BranchName = %q", plan.BranchName)
	}
	if !strings.Contains(plan.Title, "Wire timeouts") || !strings.Contains(plan.Title, "RC-T-1") {
		t.Errorf("Title = %q", plan.Title)
	}
}

func TestComposeCommitMessageIncludesProvenance(t *testing.T) {
	vp := VerifiedPatch{
		Patch: patches.CandidatePatch{
			GapID: "g1", TargetPath: "a.go", ControlID: "RC-T-1",
			Pattern: patches.PatternTimeout, LLMModel: "test", LLMSeed: 99,
		},
		Verdict: verify.Verdict{Axes: []verify.AxisMetrics{decisiveAxis(verify.AxisLatencyP99, 50)}},
	}
	plan := Compose(RunOptions{RunID: "r1"}, sampleHarness(), []VerifiedPatch{vp})
	msg := plan.Commits[0].CommitMessage
	for _, want := range []string{"orion(RC-T-1)", "timeout", "Improves: latency_p99_ms", "Gap: g1", "LLM: test seed=99"} {
		if !strings.Contains(msg, want) {
			t.Errorf("commit message missing %q\n%s", want, msg)
		}
	}
}

func TestComposeBodyContainsHarnessAndAxes(t *testing.T) {
	vp := VerifiedPatch{
		Patch: patches.CandidatePatch{GapID: "g1", TargetPath: "a.go", ControlID: "RC-T-1", Pattern: patches.PatternTimeout},
		Verdict: verify.Verdict{
			Axes:                 []verify.AxisMetrics{decisiveAxis(verify.AxisLatencyP99, 50)},
			PairedTrialsConsumed: 8,
			MaxTrials:            12,
		},
	}
	plan := Compose(RunOptions{
		RunID: "r1",
		Issue: Issue{Title: "Wire timeouts"},
	}, sampleHarness(), []VerifiedPatch{vp})
	for _, want := range []string{"## Orion run r1", "Harness configuration", "Accepted patches (1)", "latency_p99_ms"} {
		if !strings.Contains(plan.Body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}
