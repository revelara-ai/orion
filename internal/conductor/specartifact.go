package conductor

import (
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// SpecArtifact renders the WRITTEN ARTIFACT of the spec-definition phase (or-tcs.5): the full
// provenance of the spec — the initial intent that kicked it off, the grilling Q&A that shaped it,
// and the final contract organized as functional / testing / non-functional (security+reliability)
// requirements plus the assumptions. It is the durable record of HOW the spec was reached, not just
// its final form. Weight-scaled: a HEAVY thing (a substantial greenfield project or a broad
// refactor) gets a PRD-shaped document; a LIGHT thing (a small change) gets a lean design doc.
func SpecArtifact(es spec.ExecutableSpec, dialogue []specQA, heavy bool) string {
	var b strings.Builder
	kind := "Design Document"
	if heavy {
		kind = "Product Requirements (PRD)"
	}
	fmt.Fprintf(&b, "# %s — %s\n\n", kind, oneLine(es.Intent))
	if es.Hash != "" {
		fmt.Fprintf(&b, "**Spec anchor:** `%s` — the build is proven against the contract below.\n\n", es.Hash)
	}

	b.WriteString("## Intent (the initial request)\n\n")
	fmt.Fprintf(&b, "> %s\n\n", strings.TrimSpace(es.Intent))

	if len(dialogue) > 0 {
		b.WriteString("## How we got here — the grilling\n\n")
		b.WriteString("The contract below was elicited and ratified through this dialogue — the questions Orion asked and the answers you gave:\n\n")
		for _, d := range dialogue {
			fmt.Fprintf(&b, "**%s:** %s\n\n", d.Role, oneParagraph(d.Text))
		}
	}

	b.WriteString("## Requirements\n\n### Functional\n\n")
	rc := es.ResponseContract
	wrote := false
	if rc.Route != "" || rc.Port != 0 {
		fmt.Fprintf(&b, "- The service exposes `GET %s`", rc.Route)
		if rc.ContentType != "" {
			fmt.Fprintf(&b, " returning `%s`", rc.ContentType)
		}
		b.WriteString(".\n")
		wrote = true
	}
	for _, r := range es.Requirements {
		if t := strings.TrimSpace(r.Text); t != "" {
			fmt.Fprintf(&b, "- %s\n", t)
			wrote = true
		}
	}
	if !wrote {
		b.WriteString("- _(captured in the response contract below)_\n")
	}

	b.WriteString("\n### Testing (how each requirement is PROVEN)\n\n")
	b.WriteString("Every requirement is lowered to an executed behavioral case; the build is rejected unless each one passes:\n\n")
	ncases := 0
	for _, r := range es.Requirements {
		for _, c := range r.Cases {
			fmt.Fprintf(&b, "- `%s %s` → expect %d", c.Request.Method, c.Request.Path, c.Expect.Status)
			if c.Expect.ContentType != "" {
				fmt.Fprintf(&b, " `%s`", c.Expect.ContentType)
			}
			b.WriteString("\n")
			ncases++
		}
	}
	if ncases == 0 {
		b.WriteString("- The response-contract cases are the acceptance tests.\n")
	}

	b.WriteString("\n### Non-functional — security & reliability\n\n")
	b.WriteString("- **Security:** the secret + dependency scan must be clean; generated code runs only under the sandbox during proof.\n")
	b.WriteString("- **Reliability:** a reliability tier is classified from the build's scan and the deployment bar is enforced before delivery; a Red Button revokes auto-delivery.\n\n")

	if asm := specAssumptions(es); len(asm) > 0 {
		b.WriteString("## Assumptions (resolved on your behalf — confirm before relying on them)\n\n")
		for _, a := range asm {
			fmt.Fprintf(&b, "- %s\n", a)
		}
		b.WriteString("\n")
	}

	if heavy {
		b.WriteString("## Out of scope\n\nAnything not captured as a requirement, decision, or case above is out of scope for this build.\n\n")
		b.WriteString("## Further notes\n\nThis document is the durable record of the spec-definition phase; the build proves the service against the contract above and nothing else.\n")
	}
	return b.String()
}

// specQA is one developer-facing turn of the grilling dialogue (a question Orion asked or an answer
// the developer gave) — the provenance captured in the artifact.
type specQA struct{ Role, Text string }

// extractDialogue pulls the developer-facing grilling Q&A from the conversation: the agent's
// questions/framing and the developer's replies, dropping tool calls, tool results, and thoughts.
func extractDialogue(convo []llm.Message) []specQA {
	var out []specQA
	for _, m := range convo {
		if m.Role != llm.RoleUser && m.Role != llm.RoleAssistant {
			continue
		}
		var txt strings.Builder
		for _, blk := range m.Content {
			if blk.Type == llm.BlockText && strings.TrimSpace(blk.Text) != "" {
				txt.WriteString(blk.Text)
			}
		}
		if t := strings.TrimSpace(txt.String()); t != "" {
			role := "Developer"
			if m.Role == llm.RoleAssistant {
				role = "Orion"
			}
			out = append(out, specQA{Role: role, Text: t})
		}
	}
	return out
}

// specWeight classifies the spec for the artifact's framing: HEAVY (a substantial contract — a
// greenfield project or a broad refactor → a PRD) vs LIGHT (a small change → a lean design doc).
// v1 heuristic: heavy when the behavioral contract is non-trivial (>= 4 executed cases) or the
// grilling was long (>= 8 dialogue turns — a real elicitation). Tunable.
func specWeight(es spec.ExecutableSpec, dialogue []specQA) bool {
	cases := 0
	for _, r := range es.Requirements {
		cases += len(r.Cases)
	}
	return cases >= 4 || len(dialogue) >= 8
}

// oneParagraph collapses internal whitespace runs so a multi-line message renders compactly in the
// dialogue list.
func oneParagraph(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
