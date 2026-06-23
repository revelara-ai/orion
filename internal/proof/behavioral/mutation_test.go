package behavioral

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// TestMutationGateRejectsTautology: a real corpus kills the behavior-changing
// mutants (high mutation score); a tautological corpus (asserts nothing) kills
// none and is flagged as not fault-catching by the tier gate.
func TestMutationGateRejectsTautology(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if _, err := sandbox.GenerateTimeServiceFixture(dir, sandbox.GenSpec{Module: "orion-generated/svc", Route: "/time", Port: 8080, Format: "json", TimeZone: "UTC"}); err != nil {
		t.Fatalf("generate: %v", err)
	}

	realCorpus := testsynth.SynthesizeBehavioral(testsynth.Contract{Route: "/time", Format: "json", TimeZone: "UTC"})
	tautology := `package main
import "testing"
func TestContractBehavior(t *testing.T) { /* asserts nothing */ }
`
	threshold := reliabilitytier.MutationThreshold(reliabilitytier.Standard)

	rk, rt, err := MutationScore(ctx, dir, realCorpus, "handleTime")
	if err != nil {
		t.Fatalf("mutation (real): %v", err)
	}
	realScore := MutationScoreValue(rk, rt)
	if rt == 0 {
		t.Fatal("no applicable mutants — cannot measure mutation score")
	}
	if realScore < threshold {
		t.Fatalf("real corpus mutation score %.2f (%d/%d) below threshold %.2f", realScore, rk, rt, threshold)
	}

	tk, tt, err := MutationScore(ctx, dir, tautology, "handleTime")
	if err != nil {
		t.Fatalf("mutation (tautology): %v", err)
	}
	tautScore := MutationScoreValue(tk, tt)
	if tautScore >= threshold {
		t.Fatalf("tautological corpus scored %.2f (%d/%d) — should be flagged as not fault-catching", tautScore, tk, tt)
	}
	if tautScore >= realScore {
		t.Fatalf("tautology (%.2f) should score strictly worse than the real corpus (%.2f)", tautScore, realScore)
	}
}

// TestMutationThresholdsVaryByTier: throwaway < standard < critical.
func TestMutationThresholdsVaryByTier(t *testing.T) {
	tw := reliabilitytier.MutationThreshold(reliabilitytier.Throwaway)
	st := reliabilitytier.MutationThreshold(reliabilitytier.Standard)
	cr := reliabilitytier.MutationThreshold(reliabilitytier.Critical)
	if !(tw < st && st < cr) {
		t.Fatalf("thresholds not ordered throwaway<standard<critical: %.2f, %.2f, %.2f", tw, st, cr)
	}
}
