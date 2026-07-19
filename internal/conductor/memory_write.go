package conductor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/embed"
	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/modelfetch"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// chooseEmbedder is the semantic-recall decision (or-o213: opt-OUT, was opt-in).
// Explicit env wins both ways: ORION_MEMORY_EMBEDDER=off|none|0 disables;
// any other non-empty value selects that provider (with the model/path env
// vars). Unset → ON by default whenever the provisioned assets exist under
// <dataDir>/models (`orion model fetch`); not provisioned → keyword+heat
// recall, silently. provisioned is injected so the decision is testable
// without multi-hundred-MB assets.
func chooseEmbedder(env, dataDir string, provisioned func(dir string) bool) (embed.Config, bool) {
	switch env {
	case "off", "none", "0":
		return embed.Config{}, false
	case "":
		if dataDir == "" {
			return embed.Config{}, false
		}
		dir := filepath.Join(dataDir, "models")
		if !provisioned(dir) {
			return embed.Config{}, false
		}
		return embed.Config{Provider: "local", ModelPath: dir}, true
	}
	return embed.Config{
		Provider:  env,
		Model:     os.Getenv("ORION_MEMORY_EMBEDDING_MODEL"),
		ModelPath: os.Getenv("ORION_MEMORY_MODEL_PATH"),
	}, true
}

// resolveEmbedder builds the memory embedder per chooseEmbedder. A
// misconfiguration (or unloadable assets) logs and falls back to keyword+heat
// recall — it never fails a build.
func resolveEmbedder(dataDir string) (embed.Embedder, bool) {
	cfg, on := chooseEmbedder(os.Getenv("ORION_MEMORY_EMBEDDER"), dataDir, func(dir string) bool {
		ok, _ := modelfetch.VerifyQuick(dir, modelfetch.BGEBaseAssets())
		return ok
	})
	if !on {
		return nil, false
	}
	e, err := embed.New(cfg)
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
func rememberOutcome(ctx context.Context, mem *memory.Store, taskID string, report proof.Report, traj *buildTrajectory, specSlice string) error {
	if mem == nil || report.Outcome.Verdict != truthalign.Accept {
		return nil
	}
	content := summarizeOutcome(taskID, report, traj)
	if specSlice != "" {
		// or-qnto residual 2: the outcome carries the spec-slice SHAPE it
		// proved — a later recall can match on shape, not just task id.
		content += "\nspec slice: " + specSlice
	}
	_, err := mem.Write(ctx, memory.Item{
		Tier:      memory.MTM,
		Kind:      memory.KindPattern,
		Content:   content,
		TrustTier: memory.TrustProof,
		Heat:      1.0,
	})
	return err
}

// summarizeOutcome renders a proof-derived description of a proven task
// (no generation self-report, no corpus source — the trust wall holds).
// or-gb1.4: when the task converged after failures, the item carries the
// SUBSTANCE a later task can learn from — what analysis was overcome and what
// the passing attempt changed — not just the verdict line.
func summarizeOutcome(taskID string, report proof.Report, traj *buildTrajectory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Proven task %s (verdict %s, %d modes converged)", taskID, report.Outcome.Verdict, len(report.Outcome.Modes))
	if traj.overcame() {
		fmt.Fprintf(&b, "; converged on attempt %d", traj.Attempts)
		fmt.Fprintf(&b, "\novercame: %s", traj.Overcame[len(traj.Overcame)-1])
		if traj.ChangeSummary != "" {
			fmt.Fprintf(&b, "\npassing attempt changed: %s", traj.ChangeSummary)
		}
	}
	return b.String()
}

// proposeCandidate writes a self-evolution CANDIDATE after a proof-passed task (or-ykz.8): a
// generation-tier, active=false (Candidate) LTM item capturing the successful approach for
// later reuse. This is the mechanism that GENERATES self-evolution candidates from passing
// runs. It is doubly contained: TrustGeneration (an agent-proposed item, never a proof input)
// AND Candidate (excluded from active recall + vector indexing) until the SkillEval/activation
// lifecycle promotes it (default off; or-lrr). Best-effort: a write miss never fails a build.
func proposeCandidate(ctx context.Context, mem *memory.Store, taskID string, report proof.Report, traj *buildTrajectory) error {
	if mem == nil || report.Outcome.Verdict != truthalign.Accept {
		return nil
	}
	_, err := mem.Write(ctx, memory.Item{
		Tier:      memory.LTM,
		Kind:      memory.KindProcedure,
		Content:   summarizeCandidate(taskID, report, traj),
		TrustTier: memory.TrustGeneration, // untrusted proposal until SkillEval activates it
		Candidate: true,                   // active=false: not surfaced in recall yet
		Heat:      0.5,
	})
	return err
}

// summarizeCandidate renders the candidate body: the PROCEDURE TRAJECTORY a
// later build could reuse (or-gb1.4) — what failed, what the passing attempt
// changed, and what was proven — not a contentless template. It is an
// agent-domain proposal ("what worked"), not a proof fact — so it is written
// generation-tier and quarantined.
func summarizeCandidate(taskID string, report proof.Report, traj *buildTrajectory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "candidate procedure from proven task %s (%d modes, verdict %s) — review for reuse", taskID, len(report.Outcome.Modes), report.Outcome.Verdict)
	if traj.overcame() {
		fmt.Fprintf(&b, "\nprocedure trajectory (%d attempts):", traj.Attempts)
		for i, o := range traj.Overcame {
			fmt.Fprintf(&b, "\n  attempt %d failed: %s", i+1, o)
		}
		if traj.ChangeSummary != "" {
			fmt.Fprintf(&b, "\n  passing fix: %s", traj.ChangeSummary)
		}
	} else {
		b.WriteString("\nproven on the first attempt — no refinement was needed")
	}
	return b.String()
}

// rememberFailure writes failure-analysis memory on a non-Accept verdict so the NEXT
// attempt can avoid the same mistake — WITHOUT poisoning. It splits the record across the
// trust wall (Recall design §3): the structured proof FACTS (which modes dissented, key
// per-mode metrics) are harness-derived, so they are TrustProof — trusted context the next
// attempt may rely on. The agent NARRATIVE ("what went wrong / try X"), when one is
// supplied, is an untrusted self-report, so it is TrustGeneration — quarantined: the
// context engine renders it only in the UNTRUSTED block and it can never enter a proof
// prompt. Accept is handled by rememberOutcome. Best-effort: a write miss never fails a build.
func rememberFailure(ctx context.Context, mem *memory.Store, taskID string, report proof.Report, narrative, analysis string) error {
	if mem == nil {
		return nil
	}
	if v := report.Outcome.Verdict; v != truthalign.Reject && v != truthalign.Inconclusive {
		return nil
	}
	// or-gb1.3: the causal WHY (analyzeFailure's harness-derived analysis —
	// failing cases, unexecuted cases, per-mode diagnostics) persists as a
	// TRUSTED item, so a sibling or later task can avoid the trap instead of
	// re-deriving it. Clipped: the analysis is evidence, not a transcript.
	if a := strings.TrimSpace(analysis); a != "" {
		if len(a) > 1500 {
			a = a[:1500] + "…"
		}
		if _, err := mem.Write(ctx, memory.Item{
			Tier:      memory.MTM,
			Kind:      memory.KindFailure,
			Content:   fmt.Sprintf("causal analysis (task %s): %s", taskID, a),
			TrustTier: memory.TrustProof, // harness-derived, never an agent self-report
			Heat:      1.0,
		}); err != nil {
			return err
		}
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
