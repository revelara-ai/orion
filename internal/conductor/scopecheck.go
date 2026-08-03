package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/pkg/llm"
)

// NativeScopeChecker (or-kjcm.6) judges one proposed requirement against the
// ratified non-goals with a single forced tool call. The judge only REPORTS;
// the deterministic gate in AddRequirementScoped decides (refuse or record),
// and a judge failure fails open there — scope defense degrades, requirement
// capture never breaks.
func NativeScopeChecker(provider llm.Provider) orchestrator.ScopeChecker {
	return func(ctx context.Context, requirement string, nonGoals []orchestrator.NonGoal) (*orchestrator.ScopeConflict, error) {
		tool := llm.Tool{
			Name:        "report_scope_check",
			Description: "Report whether the requirement crosses a ratified non-goal.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"conflict":{"type":"boolean"},"non_goal":{"type":"string","description":"the crossed non-goal, VERBATIM from the list (empty when no conflict)"},"reason":{"type":"string","description":"one line on why it crosses (empty when no conflict)"}},"required":["conflict"]}`),
		}
		var b strings.Builder
		b.WriteString("Ratified non-goals (work consciously ruled OUT of this project):\n")
		for _, n := range nonGoals {
			b.WriteString("- " + n.Statement)
			if n.Rationale != "" {
				b.WriteString(" (why: " + n.Rationale + ")")
			}
			b.WriteByte('\n')
		}
		b.WriteString("\nProposed requirement: " + requirement + "\nReport your judgment now.")
		resp, err := provider.Chat(ctx, llm.ChatRequest{
			System: "You judge whether a proposed requirement would build something a project's ratified non-goals consciously ruled out. " +
				"Report conflict=true ONLY when the requirement clearly asks for excluded work — an overlap of vocabulary is not a conflict; the requirement must actually deliver what the non-goal excludes. When in doubt, report no conflict.",
			Tools:     []llm.Tool{tool},
			Messages:  []llm.Message{llm.TextMessage(llm.RoleUser, b.String())},
			MaxTokens: 300,
		})
		if err != nil {
			return nil, err
		}
		for _, tu := range resp.ToolUses() {
			if tu.Name != "report_scope_check" {
				continue
			}
			var out struct {
				Conflict bool   `json:"conflict"`
				NonGoal  string `json:"non_goal"`
				Reason   string `json:"reason"`
			}
			if err := json.Unmarshal(tu.Input, &out); err != nil {
				return nil, fmt.Errorf("scope check: decode: %w", err)
			}
			if !out.Conflict {
				return nil, nil
			}
			return &orchestrator.ScopeConflict{NonGoal: out.NonGoal, Reason: out.Reason}, nil
		}
		return nil, fmt.Errorf("scope check: model returned no report_scope_check call")
	}
}
