// Package harnesseval is PRINCE's tier-2 eval for Orion itself (or-gb1.2):
// a continuous pass over the DEPLOYED loop's own audit trail — run_events
// phase transitions + the spend ledger's model attribution — reporting
// quality TRENDS stratified by model. The per-run proof gate protects each
// artifact; it structurally cannot say "the loop degraded since the model
// swap" — this job can. Signals are HARNESS-DERIVED ONLY (phase outcomes the
// pipeline itself recorded); nothing here reads or trusts an agent's
// self-report, and flagging is DETERMINISTIC (margin + minimum-N; an LLM may
// annotate a trend someday, it never sets the alarm).
package harnesseval

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// RunSignals are one run's harness-derived outcomes.
type RunSignals struct {
	RunID        string
	Model        string // dominant model_ref from the spend ledger; "unknown" when unattributed
	SkillVersion string // reserved stratum (stamped by promotion runs); "" today
	At           time.Time
	ProveTotal   int // Prove phase events
	ProveGreen   int // ... that completed done
	DriftWarns   int // SystemValidate warns (drift incidence)
	Escalations  int // Escalate phase events
}

// Metrics aggregate a stratum.
type Metrics struct {
	Runs            int
	ProvePassRate   float64 // green Prove events / all Prove events
	DriftRate       float64 // runs with a drift warn / runs
	EscalationRate  float64 // runs with an escalation / runs
	proveGreen, prv int
	drifted, escal  int
}

// Collect derives per-run signals from the audit substrate for a project.
// Harness rows only: run_events (phase outcomes) + spend_ledger (model).
func Collect(ctx context.Context, store *contextstore.Store, projectID string) ([]RunSignals, error) {
	runIDs, err := store.ListRunIDs(ctx, projectID, 1000)
	if err != nil {
		return nil, err
	}
	models, err := store.DominantModelByRun(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var out []RunSignals
	for _, runID := range runIDs {
		events, err := store.ListRunEventsAfter(ctx, runID, 0)
		if err != nil {
			return nil, err
		}
		if len(events) == 0 {
			continue
		}
		sig := RunSignals{RunID: runID, Model: models[runID]}
		if sig.Model == "" {
			sig.Model = "unknown"
		}
		if at, perr := time.Parse(time.RFC3339Nano, events[0].CreatedAt); perr == nil {
			sig.At = at
		}
		for _, e := range events {
			switch e.Phase {
			case "Prove":
				sig.ProveTotal++
				if e.Status == "done" {
					sig.ProveGreen++
				}
			case "SystemValidate":
				if e.Status == "warn" {
					sig.DriftWarns++
				}
			case "Escalate":
				sig.Escalations++
			}
		}
		out = append(out, sig)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// StratumKey selects the stratification axis.
type StratumKey func(RunSignals) string

// ByModel stratifies on the run's dominant model; ByModelAndSkill adds the
// skill-version dimension (empty version folds into the model stratum).
func ByModel(s RunSignals) string { return s.Model }
func ByModelAndSkill(s RunSignals) string {
	if s.SkillVersion == "" {
		return s.Model
	}
	return s.Model + "|skill:" + s.SkillVersion
}

// Aggregate folds run signals into per-stratum metrics.
func Aggregate(sigs []RunSignals, key StratumKey) map[string]Metrics {
	out := map[string]Metrics{}
	for _, s := range sigs {
		k := key(s)
		m := out[k]
		m.Runs++
		m.prv += s.ProveTotal
		m.proveGreen += s.ProveGreen
		if s.DriftWarns > 0 {
			m.drifted++
		}
		if s.Escalations > 0 {
			m.escal++
		}
		out[k] = m
	}
	for k, m := range out {
		if m.prv > 0 {
			m.ProvePassRate = float64(m.proveGreen) / float64(m.prv)
		}
		m.DriftRate = float64(m.drifted) / float64(m.Runs)
		m.EscalationRate = float64(m.escal) / float64(m.Runs)
		out[k] = m
	}
	return out
}

// Regression is one deterministically-flagged downward trend.
type Regression struct {
	Stratum        string
	Signal         string
	Before, After  float64
	SuspectedCause string
}

func (r Regression) String() string {
	return fmt.Sprintf("%s: %s regressed %.2f → %.2f (%s)", r.Stratum, r.Signal, r.Before, r.After, r.SuspectedCause)
}

// FlagRegressions compares an earlier window against a later one, per
// stratum. Deterministic Gold-margin significance: a signal flags ONLY when
// both windows carry at least minN runs AND the degradation exceeds margin —
// noise never pages. Cause heuristic: a stratum absent from the earlier
// window while an established one degrades reads as a model swap; otherwise
// a skill change / environment shift.
func FlagRegressions(before, after map[string]Metrics, minN int, margin float64) []Regression {
	newStrata := false
	for k := range after {
		if _, ok := before[k]; !ok {
			newStrata = true
		}
	}
	cause := "skill change or environment shift in the window"
	if newStrata {
		cause = "model swap (a new model stratum appears in the later window)"
	}
	var regs []Regression
	keys := make([]string, 0, len(before))
	for k := range before {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b, a := before[k], after[k]
		if b.Runs < minN || a.Runs < minN {
			continue
		}
		type sig struct {
			name           string
			before, after  float64
			worseWhenLower bool
		}
		for _, s := range []sig{
			{"prove_pass_rate", b.ProvePassRate, a.ProvePassRate, true},
			{"drift_rate", b.DriftRate, a.DriftRate, false},
			{"escalation_rate", b.EscalationRate, a.EscalationRate, false},
		} {
			degraded := s.before-s.after > margin
			if !s.worseWhenLower {
				degraded = s.after-s.before > margin
			}
			if degraded {
				regs = append(regs, Regression{Stratum: k, Signal: s.name, Before: s.before, After: s.after, SuspectedCause: cause})
			}
		}
	}
	return regs
}

// Report runs the whole tier-2 pass: collect, split the run stream in half
// (earlier window vs later window), aggregate per model stratum, and flag.
// Returns a human-readable report + the regressions for escalation.
func Report(ctx context.Context, store *contextstore.Store, projectID string, minN int, margin float64) (string, []Regression, error) {
	sigs, err := Collect(ctx, store, projectID)
	if err != nil {
		return "", nil, err
	}
	if len(sigs) < 2 {
		return fmt.Sprintf("harness eval: %d run(s) recorded — not enough for a trend", len(sigs)), nil, nil
	}
	half := len(sigs) / 2
	before := Aggregate(sigs[:half], ByModelAndSkill)
	after := Aggregate(sigs[half:], ByModelAndSkill)
	regs := FlagRegressions(before, after, minN, margin)

	var sb strings.Builder
	fmt.Fprintf(&sb, "harness eval: %d runs, window split %d/%d (margin %.2f, min-N %d)\n", len(sigs), half, len(sigs)-half, margin, minN)
	keys := make([]string, 0, len(after))
	for k := range after {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		m := after[k]
		fmt.Fprintf(&sb, "  %s: runs=%d prove_pass=%.2f drift=%.2f escalation=%.2f\n", k, m.Runs, m.ProvePassRate, m.DriftRate, m.EscalationRate)
	}
	if len(regs) == 0 {
		sb.WriteString("  no significant regressions\n")
	}
	for _, r := range regs {
		fmt.Fprintf(&sb, "  REGRESSION %s\n", r)
	}
	return sb.String(), regs, nil
}
