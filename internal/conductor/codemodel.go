package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/brownfield"
	"github.com/revelara-ai/orion/pkg/llm"
)

// Domain is one functional capability of the system + the real packages implementing it.
type Domain struct {
	Name     string   `json:"name"`
	Purpose  string   `json:"purpose"`
	Packages []string `json:"packages"`
}

// FunctionalModel is the SEMANTIC tier of the codebase model: the system's functional
// decomposition (what it DOES — domains/capabilities), layered on the deterministic
// RepoMap (what exists — packages/API/imports). LLM-PROPOSED and developer-reviewable,
// not asserted-as-truth; every package it names is GROUNDED against the real map (a
// hallucinated package is dropped), so the structure is always accurate even when the
// interpretation is the model's.
type FunctionalModel struct {
	Summary    string   `json:"summary"`
	Domains    []Domain `json:"domains"`
	Ungrounded []string `json:"ungrounded,omitempty"` // packages the model named that don't exist (dropped)
}

// AnalyzeFunctionalModel asks the model to decompose the codebase (from its RepoMap
// digest) into functional domains, then GROUNDS the result against the real packages.
func AnalyzeFunctionalModel(ctx context.Context, provider llm.Provider, m brownfield.RepoMap) (FunctionalModel, error) {
	tool := llm.Tool{
		Name:        "report_functional_model",
		Description: "Report the codebase's functional decomposition: a one-line system summary and its domains (cohesive capabilities), each with a purpose and the REAL package dirs that implement it.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{
			"summary":{"type":"string"},
			"domains":{"type":"array","items":{"type":"object","properties":{
				"name":{"type":"string"},"purpose":{"type":"string"},
				"packages":{"type":"array","items":{"type":"string"}}},"required":["name","purpose","packages"]}}
		},"required":["summary","domains"]}`),
	}
	resp, err := provider.Chat(ctx, llm.ChatRequest{
		System:   functionalModelPrompt,
		Tools:    []llm.Tool{tool},
		Messages: []llm.Message{llm.TextMessage(llm.RoleUser, m.Digest()+"\n\nCall report_functional_model. Use ONLY package dirs that appear above.")},
	})
	if err != nil {
		return FunctionalModel{}, err
	}
	for _, tu := range resp.ToolUses() {
		if tu.Name == "report_functional_model" {
			var fm FunctionalModel
			if err := json.Unmarshal(tu.Input, &fm); err != nil {
				return FunctionalModel{}, fmt.Errorf("functional model: decode: %w", err)
			}
			return groundFunctionalModel(fm, m), nil
		}
	}
	return FunctionalModel{Summary: "no functional model returned"}, nil
}

// groundFunctionalModel drops any package the model named that is NOT in the real
// RepoMap (no invented structure), recording them in Ungrounded for transparency.
func groundFunctionalModel(fm FunctionalModel, m brownfield.RepoMap) FunctionalModel {
	actual := make(map[string]bool, len(m.Packages))
	for _, p := range m.Packages {
		actual[p.Dir] = true
	}
	seenBad := map[string]bool{}
	out := fm
	out.Domains = nil
	for _, d := range fm.Domains {
		var kept []string
		for _, pkg := range d.Packages {
			if actual[pkg] {
				kept = append(kept, pkg)
			} else if !seenBad[pkg] {
				seenBad[pkg] = true
				out.Ungrounded = append(out.Ungrounded, pkg)
			}
		}
		d.Packages = kept
		out.Domains = append(out.Domains, d)
	}
	sort.Strings(out.Ungrounded)
	return out
}

// Digest renders the functional model for the grill / developer review.
func (fm FunctionalModel) Digest() string {
	var b strings.Builder
	b.WriteString("# Functional model (proposed — review)\n")
	if fm.Summary != "" {
		fmt.Fprintf(&b, "%s\n", fm.Summary)
	}
	for _, d := range fm.Domains {
		fmt.Fprintf(&b, "\n## %s\n%s\n", d.Name, d.Purpose)
		if len(d.Packages) > 0 {
			fmt.Fprintf(&b, "packages: %s\n", strings.Join(d.Packages, ", "))
		}
	}
	if len(fm.Ungrounded) > 0 {
		fmt.Fprintf(&b, "\n_(dropped %d package name(s) not in the codebase: %s)_\n", len(fm.Ungrounded), strings.Join(fm.Ungrounded, ", "))
	}
	return b.String()
}

const functionalModelPrompt = `You map a codebase to its FUNCTIONAL domains. You receive a deterministic codebase map (packages, exported APIs, internal import edges, and the high-blast-radius "foundations").

Produce: a one-line summary of what the system does, and its DOMAINS — cohesive capabilities, each grouping the packages that implement it. A domain is a responsibility ("proof harness", "spec elicitation", "git delivery"), not a single package unless it stands alone. Infer purpose from package names, APIs, and the dependency structure.

Rules:
- Use ONLY package dirs that appear in the map. Never invent a package.
- Every package should land in at most one domain; foundational/shared packages may be their own domain.
- Be accurate and concise. This is a PROPOSAL the developer reviews; it grounds where their intent lands, so do not overstate.
Call report_functional_model.`
