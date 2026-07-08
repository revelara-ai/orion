package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/pkg/llm"
)

// NativeModuleProposer is the LLM adapter for the semantic ModuleProposer
// (or-809). It lives here (not in the deterministic decomposer package) beside
// NativeAligner: the proposer is assumed ADVERSARIAL, so its output is gated by
// decomposer's deterministic ReconcileFloor + CoverageGate + proof-time
// EnforceObligations — this function only PROPOSES.
func NativeModuleProposer(provider llm.Provider) decomposer.ModuleProposer {
	return func(ctx context.Context, es spec.ExecutableSpec, projectType string, floor []completeness.Dimension) ([]decomposer.ProposedModule, error) {
		tool := llm.Tool{
			Name:        "report_modules",
			Description: "Propose the semantic vertical-slice modules the build should produce for this spec.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"modules":{"type":"array","items":{"type":"object","properties":{"key":{"type":"string"},"title":{"type":"string"},"proof_obligation":{"type":"string"},"file_scope":{"type":"string"},"covers":{"type":"array","items":{"type":"string"}},"depends_on":{"type":"array","items":{"type":"string"}}},"required":["key","title","proof_obligation","covers"]}}},"required":["modules"]}`),
		}
		resp, err := provider.Chat(ctx, llm.ChatRequest{
			System:   moduleProposerSystemPrompt,
			Tools:    []llm.Tool{tool},
			Messages: []llm.Message{llm.TextMessage(llm.RoleUser, renderModuleProposeTask(es, projectType, floor))},
		})
		if err != nil {
			return nil, err
		}
		for _, tu := range resp.ToolUses() {
			if tu.Name == "report_modules" {
				var out struct {
					Modules []decomposer.ProposedModule `json:"modules"`
				}
				if err := json.Unmarshal(tu.Input, &out); err != nil {
					return nil, fmt.Errorf("propose: decode modules: %w", err)
				}
				return out.Modules, nil
			}
		}
		return nil, fmt.Errorf("propose: model returned no report_modules call")
	}
}

const moduleProposerSystemPrompt = `You are Orion's module proposer. You receive a ratified, anchored spec and MUST propose the SEMANTIC VERTICAL-SLICE modules the build should produce.

Rules:
- Slice VERTICALLY (a feature end-to-end), never horizontally (never a "write all the handlers" layer).
- Each module declares what it PROVES (proof_obligation), the files it owns (file_scope, for path-lease isolation), the requirement dimensions and behavioral case IDs it COVERS, and its prerequisite module keys (depends_on) — the DAG edges.
- The reliability FLOOR dimensions listed in the task are MANDATORY: between them, your modules MUST cover every floor dimension. You may choose WHICH module owns each — you may re-slice — but you may NEVER drop one.
- Every behavioral case id listed MUST be covered by some module's obligation.
- Do NOT propose an "acceptance" module — Orion synthesizes the whole-intent acceptance bookend itself.
Call report_modules with the full module set.`

func renderModuleProposeTask(es spec.ExecutableSpec, projectType string, floor []completeness.Dimension) string {
	var b strings.Builder
	b.WriteString("# Intent\n")
	b.WriteString(strings.TrimSpace(es.Intent))
	fmt.Fprintf(&b, "\n\n# Project type\n%s\n", projectType)
	b.WriteString("\n# MANDATORY floor dimensions (cover every one)\n")
	for _, d := range floor {
		fmt.Fprintf(&b, "- %s\n", d)
	}
	if ids := es.ResponseContract.RequiredCaseIDs(); len(ids) > 0 {
		sort.Strings(ids)
		b.WriteString("\n# Behavioral case IDs (each must be covered)\n")
		for _, id := range ids {
			fmt.Fprintf(&b, "- %s\n", id)
		}
	}
	if r := es.ResponseContract.Route; r != "" {
		fmt.Fprintf(&b, "\n# Contract\nGET %s · port %d · %s\n", r, es.ResponseContract.Port, es.ResponseContract.Format())
	}
	return b.String()
}

// normalizeSeverity lowercases + trims an LLM-returned alignment severity so an
// exact-string comparison ("high"/"medium") tolerates casing/whitespace variants
// (or-809: a stray "High" must not silently skip the block).
func normalizeSeverity(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
