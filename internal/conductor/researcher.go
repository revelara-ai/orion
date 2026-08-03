package conductor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/harness"
	"github.com/revelara-ai/orion/internal/harnessconfig"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
)

// NativeResearcher (or-kjcm.4) resolves a research-mode open question with a
// bounded, READ-ONLY nested loop — the same harness the spawn_subagent tool
// uses, scoped to the read-only research toolset (workspace reads, web,
// revelara.ai research tools). It returns the finding, or an error when the
// lookup could not settle the question — the orchestrator then downgrades the
// question to the developer; a researcher never guesses.
func NativeResearcher(provider llm.Provider, r *tools.Registry, c *orchestrator.Conductor, emit func(acp.Update)) orchestrator.Researcher {
	return func(ctx context.Context, q contextstore.OpenQuestion) (string, error) {
		sub := tools.NewRegistry()
		var granted []string
		for _, name := range subagentDefaultTools(r) {
			if t, ok := r.Get(name); ok {
				sub.Register(t)
				granted = append(granted, name)
			}
		}
		if len(granted) == 0 {
			return "", fmt.Errorf("research: no read-only tools available")
		}
		label := "research: " + q.Key
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		loop := harness.Loop{
			Provider: provider,
			Tools:    sub,
			System: fmt.Sprintf(`You are Orion's research agent: you settle ONE factual question by lookup so the developer is never asked what can be read.

Available tools: %s.

- Find the fact — in the workspace (read_file/grep/glob), the web, or the research tools — and STOP.
- Your final message IS the finding: the answer, where you found it, and any caveat. Keep it under a paragraph.
- If the fact cannot be established from what you can read, say exactly "UNRESOLVED" and one line why — never guess, never invent. The question then goes to the developer.`, strings.Join(granted, ", ")),
			Supervisor: harness.Supervisor{MaxIterations: harnessconfig.ToolTurns("RESEARCH", 20), Budget: c.Budget(), CapHint: "the research loop is discarded; its partial finding (if any) is returned"},
			Role:       "research",
		}
		if emit != nil {
			emit(acp.Activity(label, "looking up", 1, "running"))
			defer emit(acp.Activity(label, "", 1, "done"))
		}
		convo, resp, runErr := loop.Run(cctx, []llm.Message{llm.TextMessage(llm.RoleUser, q.Question)}, nil)
		answer := ""
		if resp != nil {
			answer = strings.TrimSpace(resp.Text())
		}
		if answer == "" {
			answer = strings.TrimSpace(lastAssistantText(convo))
		}
		if runErr != nil && answer == "" {
			return "", fmt.Errorf("research: %w", runErr)
		}
		if answer == "" || strings.HasPrefix(answer, "UNRESOLVED") {
			return "", fmt.Errorf("research: question not settled by lookup: %s", firstLine(answer))
		}
		return answer, nil
	}
}
