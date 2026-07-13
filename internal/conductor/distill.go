package conductor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/pkg/llm"
)

// or-gb1.4 DISTILL: an opt-in LLM pass that turns a task's refinement
// trajectory (what failed, what fixed it) into ONE transferable rule a later
// build can reuse. Doubly contained like every agent proposal: TrustGeneration
// (never a proof input) AND Candidate (excluded from active recall) until the
// SkillEval lifecycle activates it. NEVER in the default build path: it runs
// only when ORION_MEMORY_DISTILL=1 AND a distill provider was wired.
//
// Re-derivation compounds for free: memory writes are content-addressed, so
// the same rule re-derived on a later task/run refreshes the item's heat and
// recency instead of duplicating it.

// distillLLM is the injected provider hook. Set by the runner that owns the
// LLM (SetDistillProvider); nil means the pass is silently off.
var distillLLM llm.Provider

// SetDistillProvider wires the LLM used by the opt-in distillation pass
// (or-gb1.4). Call it where the Generator's provider is constructed; a nil
// provider disables the pass.
func SetDistillProvider(p llm.Provider) { distillLLM = p }

func distillEnabled() bool { return os.Getenv("ORION_MEMORY_DISTILL") == "1" }

// distillRule distills a transferable rule from a proven task's trajectory and
// writes it as a generation-tier CANDIDATE. Best-effort: any miss logs and
// returns — it never fails a green build.
func distillRule(ctx context.Context, mem *memory.Store, taskID string, report proof.Report, traj *buildTrajectory) {
	if mem == nil || !distillEnabled() || distillLLM == nil || !traj.overcame() {
		return
	}
	rule, err := distillWithLLM(ctx, distillLLM, taskID, report, traj)
	if err != nil || strings.TrimSpace(rule) == "" {
		slog.Warn("memory distillation skipped", "task", taskID, "err", err)
		return
	}
	if len(rule) > 1000 {
		rule = rule[:1000] + "…"
	}
	if _, err := mem.Write(ctx, memory.Item{
		Tier:      memory.LTM,
		Kind:      memory.KindRule,
		Content:   "distilled rule: " + strings.TrimSpace(rule),
		TrustTier: memory.TrustGeneration, // an LLM proposal, never a proof input
		Candidate: true,                   // quarantined from active recall until verified/activated
		Heat:      0.5,
	}); err != nil {
		slog.Warn("memory distillation write failed", "task", taskID, "err", err)
	}
}

// distillWithLLM asks the model for one transferable rule over the
// harness-derived evidence. The output is treated as UNTRUSTED text.
func distillWithLLM(ctx context.Context, provider llm.Provider, taskID string, report proof.Report, traj *buildTrajectory) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Task %s was proven (verdict %s) after %d attempts.\n", taskID, report.Outcome.Verdict, traj.Attempts)
	for i, o := range traj.Overcame {
		fmt.Fprintf(&b, "Attempt %d causal failure analysis:\n%s\n", i+1, o)
	}
	if traj.ChangeSummary != "" {
		fmt.Fprintf(&b, "The passing attempt changed: %s\n", traj.ChangeSummary)
	}
	b.WriteString("\nState ONE general, transferable rule (1-3 sentences) a future code-generation attempt should follow to avoid this failure class. Answer with the rule only.")
	resp, err := provider.Chat(ctx, llm.ChatRequest{
		System:    "You distill build-failure trajectories into single transferable engineering rules.",
		Messages:  []llm.Message{llm.TextMessage(llm.RoleUser, b.String())},
		MaxTokens: 400,
	})
	if err != nil {
		return "", err
	}
	return resp.Text(), nil
}
