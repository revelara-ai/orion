package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
	"github.com/revelara-ai/orion/pkg/llm"
)

// GoalHazardDraft is the LLM-proposed goal-altitude hazard analysis: the
// losses the project must not cause and candidate loss scenarios, drafted
// from the ratified goals document (or-045a.3). PROPOSED only — the developer
// ratifies via ratify_losses.
type GoalHazardDraft struct {
	Losses    []stpa.Loss         `json:"losses"`
	Scenarios []stpa.LossScenario `json:"scenarios"`
}

// AnalyzeGoalHazards drafts losses + loss scenarios FROM the ratified goals
// (goal altitude — what the project must achieve defines what losing looks
// like), grounded by the intent. Same adversarial-adapter shape as
// AnalyzeSTAMPBaseline; the deterministic Questionnaire gates ratification.
func AnalyzeGoalHazards(ctx context.Context, provider llm.Provider, intent, goals string) (GoalHazardDraft, error) {
	tool := llm.Tool{
		Name:        "report_hazards",
		Description: "Report the goal-altitude loss analysis: losses (unacceptable outcomes measured against the GOALS) and loss scenarios (trigger + sustaining condition that would cause a loss).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{
			"losses":{"type":"array","minItems":1,"items":{"type":"object","properties":{"id":{"type":"string"},"description":{"type":"string"}},"required":["id","description"]}},
			"scenarios":{"type":"array","minItems":1,"items":{"type":"object","properties":{"id":{"type":"string"},"trigger":{"type":"string"},"sustaining_condition":{"type":"string"},"loss":{"type":"string","description":"the loss id this scenario causes"}},"required":["id","trigger","loss"]}}
		},"required":["losses","scenarios"]}`),
	}
	resp, err := provider.Chat(ctx, llm.ChatRequest{
		System: "You are Orion's goal-altitude hazard analyst (STPA losses phase). From the project GOALS — not code, which does not exist yet — derive: (1) LOSSES: the unacceptable outcomes, each phrased as what a stakeholder loses when a goal fails; (2) LOSS SCENARIOS: concrete trigger + sustaining-condition pairs that would cause each loss. Stay at goal altitude; no implementation detail. At most 6 losses and 8 scenarios.",
		Tools:  []llm.Tool{tool},
		Messages: []llm.Message{llm.TextMessage(llm.RoleUser,
			fmt.Sprintf("Intent: %s\n\nRatified goals:\n%s\n\nReport the loss analysis now.", intent, goals))},
		MaxTokens: 2000,
	})
	if err != nil {
		return GoalHazardDraft{}, err
	}
	for _, tu := range resp.ToolUses() {
		if tu.Name != "report_hazards" {
			continue
		}
		var d GoalHazardDraft
		if err := json.Unmarshal(tu.Input, &d); err != nil {
			return GoalHazardDraft{}, fmt.Errorf("goal hazards: decode: %w", err)
		}
		return d, nil
	}
	return GoalHazardDraft{}, fmt.Errorf("goal hazards: model returned no report_hazards call")
}

// RenderGoalHazardDraft formats the draft for developer review.
func RenderGoalHazardDraft(d GoalHazardDraft) string {
	var b strings.Builder
	b.WriteString("LOSS ANALYSIS DRAFT (review with the developer; ratify_losses once confirmed):\n\nLosses:\n")
	for _, l := range d.Losses {
		fmt.Fprintf(&b, "- %s: %s\n", l.ID, l.Description)
	}
	b.WriteString("\nLoss scenarios:\n")
	for _, s := range d.Scenarios {
		fmt.Fprintf(&b, "- %s → %s: trigger %q, sustained by %q\n", s.ID, s.Loss, s.Trigger, s.SustainingCondition)
	}
	return strings.TrimRight(b.String(), "\n")
}
