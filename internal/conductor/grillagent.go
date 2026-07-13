package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/harnessconfig"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/pkg/llm"
)

// NativeGrillAgent (or-794, V3 Step 5): the open-ended elicitation driver —
// relentless listening at INTENT altitude, not the checklist's operation
// altitude. The grill PROPOSES questions; the deterministic driver seam
// (orchestrator.openDecisions) decides what surfaces, the floor never drops,
// and spec.Compile still refuses to anchor with unresolved floor dimensions.
// goalsFn supplies the RATIFIED goals document for prompt grounding ("" when
// none) — nil is valid (no goals context).
func NativeGrillAgent(provider llm.Provider, goalsFn func(context.Context) string) orchestrator.GrillAgent {
	return func(ctx context.Context, intent string, resolved map[string]string, floor []completeness.OpenDecision) ([]completeness.OpenDecision, error) {
		goals := ""
		if goalsFn != nil {
			goals = goalsFn(ctx)
		}
		tool := llm.Tool{
			Name:        "report_questions",
			Description: "Report the next open-ended clarifying questions (empty when the intent is fully understood).",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"questions":{"type":"array","items":{"type":"object","properties":{"key":{"type":"string","description":"stable snake_case answer key"},"question":{"type":"string"}},"required":["key","question"]}}},"required":["questions"]}`),
		}
		system := grillSystemPrompt
		// or-kzf.2 surface: an externalized goals-phase guidance file extends
		// the grill's system prompt without recompiling (re-read per use).
		if extra := harnessconfig.Rules("grill"); extra != "" {
			system += "\n\n" + extra
		}
		resp, err := provider.Chat(ctx, llm.ChatRequest{
			System:    system,
			Tools:     []llm.Tool{tool},
			Messages:  []llm.Message{llm.TextMessage(llm.RoleUser, renderGrillTask(intent, resolved, floor, goals))},
			MaxTokens: 1500,
		})
		if err != nil {
			return nil, err
		}
		for _, tu := range resp.ToolUses() {
			if tu.Name != "report_questions" {
				continue
			}
			var out struct {
				Questions []struct {
					Key      string `json:"key"`
					Question string `json:"question"`
				} `json:"questions"`
			}
			if err := json.Unmarshal(tu.Input, &out); err != nil {
				return nil, fmt.Errorf("grill: decode questions: %w", err)
			}
			qs := make([]completeness.OpenDecision, 0, len(out.Questions))
			for i, q := range out.Questions {
				if i >= 5 {
					break // bounded per round — a grill, not an interrogation transcript
				}
				key := strings.TrimSpace(q.Key)
				if key == "" {
					continue
				}
				if !strings.HasPrefix(key, "grill.") {
					key = "grill." + key
				}
				qs = append(qs, completeness.OpenDecision{Key: key, Dimension: completeness.DimFunctional, Question: strings.TrimSpace(q.Question)})
			}
			return qs, nil
		}
		return nil, fmt.Errorf("grill: model returned no report_questions call")
	}
}

const grillSystemPrompt = `You are Orion's intent grill: relentless LISTENING, not a checklist. You receive a developer's intent plus what is already resolved. Ask ONLY the open-ended questions whose answers would change what gets built — scope boundaries, success criteria, users and their failure costs, what is explicitly OUT of scope, behavioral edge cases worth proving. Never re-ask anything resolved, never ask implementation trivia the reliability floor already covers (ports, formats, routes). At most 5 questions; report NONE when the intent is genuinely clear.`

func renderGrillTask(intent string, resolved map[string]string, floor []completeness.OpenDecision, goals string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Intent: %s\n", intent)
	if goals != "" {
		b.WriteString("\nRatified project goals (steer every question by these; probe gaps between them and the intent):\n")
		b.WriteString(goals)
		b.WriteString("\n")
	}
	if len(resolved) > 0 {
		b.WriteString("\nAlready resolved (never re-ask):\n")
		for k, v := range resolved {
			fmt.Fprintf(&b, "- %s = %s\n", k, v)
		}
	}
	if len(floor) > 0 {
		b.WriteString("\nThe reliability floor will ask these separately (never duplicate them):\n")
		for _, f := range floor {
			fmt.Fprintf(&b, "- %s: %s\n", f.Key, f.Question)
		}
	}
	b.WriteString("\nReport your open-ended questions now (or none).")
	return b.String()
}
