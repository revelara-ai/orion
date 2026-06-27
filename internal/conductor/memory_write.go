package conductor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/embed"
	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// embedderFromEnv builds the memory embedder from env — opt-in semantic recall (or-hd3.7).
// ORION_MEMORY_EMBEDDER (e.g. "local") selects the in-process GoMLX embedder;
// ORION_MEMORY_EMBEDDING_MODEL + ORION_MEMORY_MODEL_PATH point at the ONNX model. Unset →
// keyword+heat recall (no embedder, no model file needed). A misconfiguration logs and falls
// back to keyword recall — it never fails a build.
func embedderFromEnv() (embed.Embedder, bool) {
	provider := os.Getenv("ORION_MEMORY_EMBEDDER")
	if provider == "" {
		return nil, false
	}
	e, err := embed.New(embed.Config{
		Provider:  provider,
		Model:     os.Getenv("ORION_MEMORY_EMBEDDING_MODEL"),
		ModelPath: os.Getenv("ORION_MEMORY_MODEL_PATH"),
	})
	if err != nil {
		slog.Warn("memory embedder disabled; falling back to keyword+heat recall", "err", err)
		return nil, false
	}
	return e, true
}

// memMTMCapacity / memLTMCapacity bound the tiers after each task — the
// context-degradation defense (colder cognition is summarized-then-evicted; pins are never
// evicted). LTM is larger (durable cross-run patterns) but still bounded so promotion
// (or-hd3.6) can't grow it without limit. Fixed caps; config-driven tuning is a later
// refinement (like the heat weights).
const (
	memMTMCapacity = 200
	memLTMCapacity = 1000
)

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

// proposeCandidate writes a self-evolution CANDIDATE after a proof-passed task (or-ykz.8): a
// generation-tier, active=false (Candidate) LTM item capturing the successful approach for
// later reuse. This is the mechanism that GENERATES self-evolution candidates from passing
// runs. It is doubly contained: TrustGeneration (an agent-proposed item, never a proof input)
// AND Candidate (excluded from active recall + vector indexing) until the SkillEval/activation
// lifecycle promotes it (default off; or-lrr). Best-effort: a write miss never fails a build.
func proposeCandidate(ctx context.Context, mem *memory.Store, taskID string, report proof.Report) error {
	if mem == nil || report.Outcome.Verdict != truthalign.Accept {
		return nil
	}
	_, err := mem.Write(ctx, memory.Item{
		Tier:      memory.LTM,
		Kind:      memory.KindProcedure,
		Content:   summarizeCandidate(taskID, report),
		TrustTier: memory.TrustGeneration, // untrusted proposal until SkillEval activates it
		Candidate: true,                   // active=false: not surfaced in recall yet
		Heat:      0.5,
	})
	return err
}

// summarizeCandidate renders the candidate body. It is an agent-domain proposal ("what
// worked"), not a proof fact — so it is written generation-tier and quarantined.
func summarizeCandidate(taskID string, report proof.Report) string {
	return fmt.Sprintf("candidate procedure from proven task %s: approach converged %d modes (verdict %s) — review for reuse", taskID, len(report.Outcome.Modes), report.Outcome.Verdict)
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
