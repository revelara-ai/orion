package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/brownfield"
	"github.com/revelara-ai/orion/pkg/llm"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
)

// HazardRef is a baseline hazard a change must preserve.
type HazardRef struct {
	UCA    string `json:"uca"`
	Hazard string `json:"hazard"`
}

// IntentMapping is the payoff of the two baseline models: a developer's intent mapped
// onto BOTH the functional model (which components it touches + the blast radius) AND
// the STAMP baseline (which hazards it must preserve). It directs the decomposition
// (what to change + the impact) and seeds the proof obligations (the baseline hazards
// the change must not break). The component/hazard picks are the model's (grounded
// against the real packages + baseline UCAs); the blast radius is deterministic.
type IntentMapping struct {
	Intent           string
	AffectedDomains  []string    // functional domains the change lands in
	AffectedPackages []string    // packages it touches (grounded in the RepoMap)
	BlastRadius      []string    // packages transitively impacted (deterministic, from the impact tier)
	MustPreserve     []HazardRef // baseline hazards the change must not break (grounded in the STAMP baseline)
	Rationale        string
	Ungrounded       []string // package/UCA refs the model named that don't exist (dropped)
}

// MapIntent maps intent onto the functional model + STAMP baseline. One LLM call picks
// the affected packages + touched baseline UCAs (semantic); grounding + blast radius are
// deterministic.
func MapIntent(ctx context.Context, provider llm.Provider, intent string, m brownfield.RepoMap, fm FunctionalModel, baseline stpa.Model) (IntentMapping, error) {
	tool := llm.Tool{
		Name:        "report_intent_mapping",
		Description: "Map the developer's intent onto the codebase: the real packages the change will touch, and the baseline UCAs (by id) whose hazards the change must preserve.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{
			"affected_packages":{"type":"array","items":{"type":"string"}},
			"touched_ucas":{"type":"array","items":{"type":"string"}},
			"rationale":{"type":"string"}
		},"required":["affected_packages","touched_ucas","rationale"]}`),
	}
	resp, err := provider.Chat(ctx, llm.ChatRequest{
		System:   intentMapPrompt,
		Tools:    []llm.Tool{tool},
		Messages: []llm.Message{llm.TextMessage(llm.RoleUser, renderIntentTask(intent, fm, baseline))},
	})
	if err != nil {
		return IntentMapping{}, err
	}
	for _, tu := range resp.ToolUses() {
		if tu.Name == "report_intent_mapping" {
			var p struct {
				AffectedPackages []string `json:"affected_packages"`
				TouchedUCAs      []string `json:"touched_ucas"`
				Rationale        string   `json:"rationale"`
			}
			if err := json.Unmarshal(tu.Input, &p); err != nil {
				return IntentMapping{}, fmt.Errorf("intent mapping: decode: %w", err)
			}
			return groundIntentMapping(intent, p.AffectedPackages, p.TouchedUCAs, p.Rationale, m, fm, baseline), nil
		}
	}
	return IntentMapping{Intent: intent, Rationale: "no mapping returned"}, nil
}

func groundIntentMapping(intent string, pkgs, ucas []string, rationale string, m brownfield.RepoMap, fm FunctionalModel, baseline stpa.Model) IntentMapping {
	out := IntentMapping{Intent: intent, Rationale: rationale}

	realPkg := make(map[string]bool, len(m.Packages))
	for _, p := range m.Packages {
		realPkg[p.Dir] = true
	}
	affected := map[string]bool{}
	for _, pkg := range pkgs {
		if realPkg[pkg] {
			affected[pkg] = true
			out.AffectedPackages = append(out.AffectedPackages, pkg)
		} else {
			out.Ungrounded = append(out.Ungrounded, "pkg:"+pkg)
		}
	}
	sort.Strings(out.AffectedPackages)

	// Blast radius: deterministic transitive dependents of the affected packages
	// (excluding the affected set itself) — the impact tier doing the impact analysis.
	blast := map[string]bool{}
	for pkg := range affected {
		for _, d := range m.BlastRadius(pkg) {
			if !affected[d] {
				blast[d] = true
			}
		}
	}
	out.BlastRadius = sortedSet(blast)

	// Affected domains: functional domains containing any affected package.
	for _, d := range fm.Domains {
		for _, pkg := range d.Packages {
			if affected[pkg] {
				out.AffectedDomains = append(out.AffectedDomains, d.Name)
				break
			}
		}
	}
	sort.Strings(out.AffectedDomains)

	// Must-preserve hazards: ground touched UCA ids against the baseline.
	hazByUCA := make(map[string]string, len(baseline.UCAs))
	for _, u := range baseline.UCAs {
		hazByUCA[u.ID] = u.Hazard
	}
	for _, id := range ucas {
		if h, ok := hazByUCA[id]; ok {
			out.MustPreserve = append(out.MustPreserve, HazardRef{UCA: id, Hazard: h})
		} else {
			out.Ungrounded = append(out.Ungrounded, "uca:"+id)
		}
	}
	sort.Slice(out.MustPreserve, func(i, j int) bool { return out.MustPreserve[i].UCA < out.MustPreserve[j].UCA })
	sort.Strings(out.Ungrounded)
	return out
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Digest renders the work direction for the developer + as input to decomposition.
func (im IntentMapping) Digest() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Work direction — %s\n", oneLine(im.Intent))
	if im.Rationale != "" {
		fmt.Fprintf(&b, "%s\n", im.Rationale)
	}
	b.WriteString("\n## Affected (functional)\n")
	if len(im.AffectedDomains) > 0 {
		fmt.Fprintf(&b, "domains: %s\n", strings.Join(im.AffectedDomains, ", "))
	}
	fmt.Fprintf(&b, "packages to change: %s\n", joinOr(im.AffectedPackages, "(none identified)"))
	if len(im.BlastRadius) > 0 {
		fmt.Fprintf(&b, "blast radius (%d also impacted): %s\n", len(im.BlastRadius), strings.Join(capStrs(im.BlastRadius, 15), ", "))
	}
	b.WriteString("\n## Must preserve (STAMP baseline)\n")
	if len(im.MustPreserve) == 0 {
		b.WriteString("(no baseline hazards identified as touched)\n")
	}
	for _, h := range im.MustPreserve {
		fmt.Fprintf(&b, "- %s: %s\n", h.UCA, h.Hazard)
	}
	if len(im.Ungrounded) > 0 {
		fmt.Fprintf(&b, "\n_(dropped %d ungrounded ref(s): %s)_\n", len(im.Ungrounded), strings.Join(im.Ungrounded, ", "))
	}
	return b.String()
}

func capStrs(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return append(xs[:n:n], fmt.Sprintf("…+%d", len(xs)-n))
}

func joinOr(xs []string, empty string) string {
	if len(xs) == 0 {
		return empty
	}
	return strings.Join(xs, ", ")
}

func renderIntentTask(intent string, fm FunctionalModel, baseline stpa.Model) string {
	var b strings.Builder
	b.WriteString("# Developer intent\n")
	b.WriteString(strings.TrimSpace(intent))
	b.WriteString("\n\n")
	b.WriteString(fm.Digest())
	b.WriteString("\n\n# STAMP baseline UCAs (hazards the system controls — preserve those the change touches)\n")
	for _, u := range baseline.UCAs {
		fmt.Fprintf(&b, "- %s [%s on %s]: %s\n", u.ID, u.Type, u.ControlAction, u.Hazard)
	}
	b.WriteString("\nCall report_intent_mapping. Use ONLY real package dirs (from the functional model) and real UCA ids (above).")
	return b.String()
}

const intentMapPrompt = `You direct work from intent. You receive a developer's INTENT, the codebase's FUNCTIONAL model (domains → packages), and its STAMP BASELINE (the unsafe control actions whose hazards the system already controls).

Identify two things:
1. affected_packages — the REAL packages the change will touch (where the new/changed behavior lands). Be precise: the packages that actually implement the affected capability, not everything related.
2. touched_ucas — the baseline UCA ids whose HAZARDS the change could affect and must therefore PRESERVE (e.g. a change to request handling must preserve the latency/availability UCAs on that control action). Only ids the change genuinely touches.

Use ONLY real package dirs and real UCA ids. This directs the decomposition (what to change + its blast radius) and seeds the proof obligations (the baseline hazards the change must not break). Call report_intent_mapping.`
