package conductor

import (
	"context"
	"fmt"

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
