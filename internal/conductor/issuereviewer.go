package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/pkg/llm"
)

// NativeIssueReviewer is the LLM-backed adversarial issue-set reviewer
// (or-zn8, V3 Step 4). It PROPOSES findings; the deterministic
// IssueReviewGate in the orchestrator applies severity policy, corroboration,
// and the advisory→blocking rollout — the reviewer holds no verdict authority.
func NativeIssueReviewer(provider llm.Provider) decomposer.IssueReviewer {
	return func(ctx context.Context, es spec.ExecutableSpec, epic decomposer.Epic) ([]decomposer.ReviewFinding, error) {
		tool := llm.Tool{
			Name:        "report_findings",
			Description: "Report the adversarial review findings over the decomposed issue set.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"findings":{"type":"array","items":{"type":"object","properties":{"severity":{"type":"string","enum":["high","medium","low"]},"dimension":{"type":"string","enum":["contradiction","coverage-gap","dependency-order","scope-collision","testability","operability"]},"issues":{"type":"array","items":{"type":"string"}},"concern":{"type":"string"}},"required":["severity","dimension","issues","concern"]}}},"required":["findings"]}`),
		}
		resp, err := provider.Chat(ctx, llm.ChatRequest{
			System:   issueReviewerSystemPrompt,
			Tools:    []llm.Tool{tool},
			Messages: []llm.Message{llm.TextMessage(llm.RoleUser, renderIssueReviewTask(es, epic))},
		})
		if err != nil {
			return nil, err
		}
		for _, tu := range resp.ToolUses() {
			if tu.Name == "report_findings" {
				var out struct {
					Findings []decomposer.ReviewFinding `json:"findings"`
				}
				if err := json.Unmarshal(tu.Input, &out); err != nil {
					return nil, fmt.Errorf("issue review: decode findings: %w", err)
				}
				return out.Findings, nil
			}
		}
		return nil, fmt.Errorf("issue review: model returned no report_findings call")
	}
}

const issueReviewerSystemPrompt = `You are Orion's adversarial issue-set reviewer. You receive a ratified spec and the DECOMPOSED issue set that will be built from it. Your job is to find what per-issue checks structurally cannot see.

Hunt, in priority order:
1. CROSS-ISSUE CONTRADICTIONS — issue A asserts or builds X while issue B assumes or requires not-X (auth vs anonymous, sync vs async, one storage model vs another). This is your primary target.
2. Coverage gaps the mechanical gate cannot judge — a requirement nominally "covered" by an issue whose obligation would not actually prove it.
3. Dependency-order defects — an issue that needs another's output but does not depend on it, or a cycle-in-spirit.
4. Scope collisions — file scopes that force full serialization or guarantee merge conflicts.
5. Untestable obligations — an obligation no deterministic test could verify.

Rules:
- Severity discipline: HIGH means "building this plan as-is produces wrong software" — a real contradiction or a false coverage claim. Ambiguity or style is MEDIUM at most. When unsure, go LOWER.
- Name the issue KEYS involved in every finding.
- An empty findings list is a valid, good answer — do NOT invent concerns.
Call report_findings exactly once.`

func renderIssueReviewTask(es spec.ExecutableSpec, epic decomposer.Epic) string {
	var b strings.Builder
	b.WriteString("# Ratified intent\n")
	b.WriteString(strings.TrimSpace(es.Intent))
	b.WriteString("\n\n# Behavioral cases (the oracle)\n")
	for _, c := range es.ResponseContract.Cases {
		fmt.Fprintf(&b, "- %s %s → %d (case %s)\n", c.Request.Method, c.Request.Path, c.Expect.Status, c.ID)
	}
	b.WriteString("\n# Decomposed issue set\n")
	for _, t := range epic.Tasks {
		fmt.Fprintf(&b, "## %s — %s\n- obligation: %s\n- covers: %s\n- file_scope: %s\n- depends_on: %s\n",
			t.Key, t.Title, t.ProofObligation, strings.Join(t.Covers, ", "), t.FileScope, strings.Join(t.DependsOn, ", "))
	}
	return b.String()
}
