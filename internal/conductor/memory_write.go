package conductor

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// memMTMCapacity bounds the MTM tier after each task — the context-degradation
// defense (colder cognition is evicted; pins are never evicted). Fixed in slice 1
// (or-hd3.2); heat-driven tuning is slice 2 (or-hd3.3).
const memMTMCapacity = 200

// rememberOutcome writes a proof-tier pattern memory item summarizing a proven
// task, so a LATER task recalls what was proven. The fact is harness-derived, so
// it is TrustProof (it may enter a trusted prompt; it is not a generation
// self-report). Only Accept is remembered here — failure-analysis writes are
// slice 4 (or-hd3.5). Best-effort: a memory miss never fails a green build.
func rememberOutcome(ctx context.Context, mem *memory.Store, taskID string, report proof.Report) error {
	if mem == nil || report.Outcome.Verdict != truthalign.Accept {
		return nil
	}
	_, err := mem.Write(ctx, memory.Item{
		Tier:      memory.MTM,
		Kind:      memory.KindPattern,
		Content:   summarizeOutcome(taskID, report),
		TrustTier: memory.TrustProof,
		Heat:      1.0,
	})
	return err
}

// summarizeOutcome renders a compact, proof-derived description of a proven task
// (no generation self-report, no corpus source — the trust wall holds).
func summarizeOutcome(taskID string, report proof.Report) string {
	return fmt.Sprintf("Proven task %s (verdict %s, %d modes converged)", taskID, report.Outcome.Verdict, len(report.Outcome.Modes))
}

// rememberFailure writes failure-analysis memory on a non-Accept verdict so the NEXT
// attempt can avoid the same mistake — WITHOUT poisoning. It splits the record across the
// trust wall (Recall design §3): the structured proof FACTS (which modes dissented, key
// per-mode metrics) are harness-derived, so they are TrustProof — trusted context the next
// attempt may rely on. The agent NARRATIVE ("what went wrong / try X"), when one is
// supplied, is an untrusted self-report, so it is TrustGeneration — quarantined: the
// context engine renders it only in the UNTRUSTED block and it can never enter a proof
// prompt. Accept is handled by rememberOutcome. Best-effort: a write miss never fails a build.
func rememberFailure(ctx context.Context, mem *memory.Store, taskID string, report proof.Report, narrative string) error {
	if mem == nil {
		return nil
	}
	if v := report.Outcome.Verdict; v != truthalign.Reject && v != truthalign.Inconclusive {
		return nil
	}
	if _, err := mem.Write(ctx, memory.Item{
		Tier:      memory.MTM,
		Kind:      memory.KindFailure,
		Content:   summarizeFailure(taskID, report),
		TrustTier: memory.TrustProof, // harness-derived facts → trusted
		Heat:      1.0,
	}); err != nil {
		return err
	}
	if strings.TrimSpace(narrative) != "" {
		if _, err := mem.Write(ctx, memory.Item{
			Tier:      memory.MTM,
			Kind:      memory.KindFailure,
			Content:   "agent failure narrative: " + strings.TrimSpace(narrative),
			TrustTier: memory.TrustGeneration, // untrusted self-report → quarantined
			Heat:      0.5,
		}); err != nil {
			return err
		}
	}
	return nil
}

// summarizeFailure renders the proof-derived failure facts: the verdict, which modes
// dissented, and the failing modes' metrics (the degradation signal). Harness-collected, so
// trusted — never an agent self-report.
func summarizeFailure(taskID string, report proof.Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task %s FAILED (verdict %s)", taskID, report.Outcome.Verdict)
	if len(report.Outcome.Dissenting) > 0 {
		fmt.Fprintf(&b, "; dissenting: %s", strings.Join(report.Outcome.Dissenting, ", "))
	}
	for _, m := range report.Outcome.Modes {
		if m.Pass || len(m.Metrics) == 0 {
			continue
		}
		keys := make([]string, 0, len(m.Metrics))
		for k := range m.Metrics {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "; %s.%s=%.4g", m.Mode, k, m.Metrics[k])
		}
	}
	return b.String()
}
