package conductor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/contextengine"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// Memory for the brownfield change flow (or-3p5.13): the greenfield loop
// remembers and consults; orion change re-derived everything. Best-effort
// throughout — a memory miss never fails (or slows the verdict of) a change.

// openChangeMemory opens the project-scoped memory store beside the context
// store (nil on any miss).
func openChangeMemory(ctx context.Context, store *contextstore.Store) *memory.Store {
	if store == nil {
		return nil
	}
	memDir := filepath.Join(store.Dir(), "memory")
	if err := os.MkdirAll(memDir, 0o700); err != nil {
		return nil
	}
	m, err := memory.Open(memDir)
	if err != nil {
		return nil
	}
	if proj, _, perr := store.CurrentProjectSpec(ctx); perr == nil {
		m.ForProject(proj.ID)
	}
	return m
}

// changeMemoryBrief renders the recalled context (prior failures, decisions,
// patterns relevant to the intent) for the diff generator's prompt.
func changeMemoryBrief(ctx context.Context, store *contextstore.Store, mem *memory.Store, intent string) string {
	if mem == nil {
		return ""
	}
	bundle, err := contextengine.New(store, mem).WithTokenBudget(generationWindow()/16).Assemble(ctx, "", intent)
	if err != nil {
		return ""
	}
	rendered := strings.TrimSpace(bundle.Render(contextengine.DomainGeneration))
	if rendered == "" {
		return ""
	}
	return "\n\nRECALLED MEMORY (prior runs on this project — consult, don't re-derive):\n" + rendered + "\n"
}

// rememberChangeOutcome records the change verdict: a committed change writes
// the outcome pattern + extracted decisions; a failed one writes the causal
// failure so the NEXT attempt recalls it instead of re-deriving.
func rememberChangeOutcome(ctx context.Context, mem *memory.Store, repoRoot, intent string, res ChangeResult) {
	if mem == nil {
		return
	}
	if res.Committed {
		_, _ = mem.Write(ctx, memory.Item{
			Tier: memory.MTM, Kind: memory.KindPattern, TrustTier: memory.TrustProof, Heat: 1.0,
			Content: fmt.Sprintf("Proven change %q: files %s committed on branch %s (regression held; new-behavior obligations passed)",
				intent, strings.Join(res.FilesChanged, ", "), res.Branch),
		})
		if decisions := changeDecisions(ctx, repoRoot, res); len(decisions) > 0 {
			_, _ = mem.Write(ctx, memory.Item{
				Tier: memory.MTM, Kind: memory.KindDecision, TrustTier: memory.TrustProof, Heat: 1.0,
				Content: fmt.Sprintf("Decided constraints from proven change %q — later changes REUSE these: %s", intent, strings.Join(decisions, "; ")),
			})
		}
		return
	}
	analysis := res.Reason
	if d := res.FailureDigest(); d != "" {
		analysis += "\n" + d
	}
	var report proof.Report
	report.Outcome.Verdict = truthalign.Reject
	_ = rememberFailure(ctx, mem, "change: "+intent, report, "", analysis)
}

// changeDecisions extracts exported surface decisions from the committed
// change's .go files (read from the review branch — the worktree is gone).
func changeDecisions(ctx context.Context, repoRoot string, res ChangeResult) []string {
	if res.Branch == "" {
		return nil
	}
	var out []string
	for _, f := range res.FilesChanged {
		if !strings.HasSuffix(f, ".go") || strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := gitIn(ctx, repoRoot, "show", res.Branch+":"+f)
		if err != nil {
			continue
		}
		out = append(out, extractDecisions(src)...)
	}
	return out
}

// sessionMemoryBrief renders the OrionAgent session-start brief (or-3p5.13c):
// top recalled items for the active project — bounded, best-effort.
func (a *OrionAgent) sessionMemoryBrief(ctx context.Context) string {
	store := a.conductor.Store()
	mem := openChangeMemory(ctx, store)
	if mem == nil {
		return ""
	}
	defer func() { _ = mem.Close() }()
	items, err := mem.Retrieve(ctx, "", memory.MTM, memory.LTM)
	if err != nil || len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("SESSION MEMORY BRIEF (prior runs on this project — background context, consult before re-deriving):\n")
	for i, it := range items {
		if i >= 8 {
			break
		}
		c := it.Content
		if len(c) > 240 {
			c = c[:240] + "…"
		}
		fmt.Fprintf(&b, "- %s\n", c)
	}
	return b.String()
}
