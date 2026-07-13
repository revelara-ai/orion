package harnesseval

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

func evalStore(t *testing.T) (*contextstore.Store, string) {
	t.Helper()
	store, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var projID string
	if err := store.WithTx(context.Background(), func(tx *contextstore.Tx) error {
		id, cerr := tx.Projects().Create(context.Background(), "p", "intent", "greenfield")
		projID = id
		return cerr
	}); err != nil {
		t.Fatal(err)
	}
	return store, projID
}

// seedRun writes one run's audit rows: nGreen green + nWarn warn Prove
// events, optional drift/escalation, and the model's spend attribution.
func seedRun(t *testing.T, store *contextstore.Store, projID, runID, model string, nGreen, nWarn int, drift, escal bool) {
	t.Helper()
	ctx := context.Background()
	add := func(phase, status string) {
		if err := store.AppendRunEvent(ctx, contextstore.RunEvent{
			ProjectID: projID, RunID: runID, Phase: phase, Status: status, Detail: "seed",
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < nGreen; i++ {
		add("Prove", "done")
	}
	for i := 0; i < nWarn; i++ {
		add("Prove", "warn")
	}
	if drift {
		add("SystemValidate", "warn")
	}
	if escal {
		add("Escalate", "warn")
	}
	if model != "" {
		if err := store.AppendSpend(ctx, projID, runID, "generation", model, 1000, 0.01); err != nil {
			t.Fatal(err)
		}
	}
}

// TestHarnessEvalAggregatesFromAuditTrail (bead-named): the job reads ONLY
// the audit substrate and derives the metrics exactly.
func TestHarnessEvalAggregatesFromAuditTrail(t *testing.T) {
	store, projID := evalStore(t)
	seedRun(t, store, projID, "r1", "m1", 3, 1, true, false) // 0.75 pass, drift
	seedRun(t, store, projID, "r2", "m1", 4, 0, false, true) // 1.00 pass, escalation
	sigs, err := Collect(context.Background(), store, projID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs) != 2 {
		t.Fatalf("want 2 runs, got %d", len(sigs))
	}
	agg := Aggregate(sigs, ByModel)
	m := agg["m1"]
	if m.Runs != 2 || m.ProvePassRate != 7.0/8.0 || m.DriftRate != 0.5 || m.EscalationRate != 0.5 {
		t.Fatalf("aggregation wrong: %+v", m)
	}
}

// TestHarnessEvalStratifiesByModelAndSkillVersion (bead-named): mixed models
// never share a stratum; a stamped skill version splits further.
func TestHarnessEvalStratifiesByModelAndSkillVersion(t *testing.T) {
	store, projID := evalStore(t)
	seedRun(t, store, projID, "r1", "claude-x", 4, 0, false, false)
	seedRun(t, store, projID, "r2", "gemma-y", 1, 3, false, false)
	sigs, err := Collect(context.Background(), store, projID)
	if err != nil {
		t.Fatal(err)
	}
	agg := Aggregate(sigs, ByModel)
	if len(agg) != 2 || agg["claude-x"].ProvePassRate != 1.0 || agg["gemma-y"].ProvePassRate != 0.25 {
		t.Fatalf("model strata polluted each other: %+v", agg)
	}
	sigs[0].SkillVersion = "v2"
	agg = Aggregate(sigs, ByModelAndSkill)
	if _, ok := agg[sigs[0].Model+"|skill:v2"]; !ok {
		t.Fatalf("skill version must open its own stratum: %v", agg)
	}
}

// TestHarnessEvalFlagsSignificantRegressionOnly (bead-named): sub-margin
// dips and thin windows stay silent; a real drop flags with the window's
// numbers and a suspected cause.
func TestHarnessEvalFlagsSignificantRegressionOnly(t *testing.T) {
	mk := func(pass float64, runs int) Metrics {
		return Metrics{Runs: runs, ProvePassRate: pass}
	}
	// Sub-margin dip: 0.90 → 0.80 with margin 0.15 — silent.
	if regs := FlagRegressions(map[string]Metrics{"m": mk(0.90, 5)}, map[string]Metrics{"m": mk(0.80, 5)}, 3, 0.15); len(regs) != 0 {
		t.Fatalf("sub-margin dip must not flag: %+v", regs)
	}
	// Thin window: huge drop but n=2 < minN — silent.
	if regs := FlagRegressions(map[string]Metrics{"m": mk(0.90, 2)}, map[string]Metrics{"m": mk(0.10, 2)}, 3, 0.15); len(regs) != 0 {
		t.Fatalf("thin windows must not flag: %+v", regs)
	}
	// Significant drop: flags, names the numbers + cause.
	regs := FlagRegressions(map[string]Metrics{"m": mk(0.90, 5)}, map[string]Metrics{"m": mk(0.40, 5)}, 3, 0.15)
	if len(regs) != 1 || regs[0].Signal != "prove_pass_rate" {
		t.Fatalf("significant drop must flag exactly once: %+v", regs)
	}
	if s := regs[0].String(); !strings.Contains(s, "0.90") || !strings.Contains(s, "0.40") {
		t.Fatalf("flag must carry the window numbers: %s", s)
	}
	// Rising escalation rate is a regression too (worse-when-higher).
	up := FlagRegressions(
		map[string]Metrics{"m": {Runs: 5, EscalationRate: 0.1}},
		map[string]Metrics{"m": {Runs: 5, EscalationRate: 0.6}}, 3, 0.15)
	if len(up) != 1 || up[0].Signal != "escalation_rate" {
		t.Fatalf("escalation-rate rise must flag: %+v", up)
	}
}

// TestHarnessEvalUsesNoAgentSelfReportedSignals (bead-named): an agent
// writing glowing free-text into the trail changes NOTHING — only the
// harness's own phase outcomes count.
func TestHarnessEvalUsesNoAgentSelfReportedSignals(t *testing.T) {
	store, projID := evalStore(t)
	seedRun(t, store, projID, "r1", "m1", 1, 3, false, false) // 0.25 pass — poor
	// The "self-report": arbitrary agent-authored events claiming perfection.
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		if err := store.AppendRunEvent(ctx, contextstore.RunEvent{
			ProjectID: projID, RunID: "r1", Phase: "AgentClaim", Status: "done",
			Detail: fmt.Sprintf("self-eval %d: everything passed, quality 10/10", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	sigs, err := Collect(ctx, store, projID)
	if err != nil {
		t.Fatal(err)
	}
	agg := Aggregate(sigs, ByModel)
	if m := agg["m1"]; m.ProvePassRate != 0.25 {
		t.Fatalf("self-reported claims leaked into the signal: %+v", m)
	}
}
