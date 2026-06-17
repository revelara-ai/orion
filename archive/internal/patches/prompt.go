package patches

import (
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/enrichment"
	"github.com/revelara-ai/orion/internal/polaris"
)

// BuildPrompt assembles the per-gap prompt for the LLM. The prompt
// surfaces the gap location, the snapshotted controls + insights, and
// the per-pattern grammar constraints. Output policy: respond with a
// single fenced unified diff and nothing else.
//
// Prompt is intentionally compact: tighter prompts give the LLM less
// room to wander and produce shorter, more grammar-conformant diffs.
func BuildPrompt(gap Gap, ctxBlock *enrichment.IssueContextBlock) string {
	if ctxBlock == nil {
		ctxBlock = &enrichment.IssueContextBlock{}
	}
	g := GrammarFor(gap.Pattern)

	var sb strings.Builder
	fmt.Fprintf(&sb, "You are generating a single unified diff for a Go reliability gap.\n\n")
	fmt.Fprintf(&sb, "## Gap\n\n")
	fmt.Fprintf(&sb, "- **ID**: %s\n", gap.ID)
	fmt.Fprintf(&sb, "- **Pattern**: %s\n", gap.Pattern)
	fmt.Fprintf(&sb, "- **File**: %s (lines %d-%d)\n", gap.FilePath, gap.LineRange[0], gap.LineRange[1])
	fmt.Fprintf(&sb, "- **Description**: %s\n", gap.Description)
	if gap.CodeExcerpt != "" {
		fmt.Fprintf(&sb, "\n### Affected code\n\n```go\n%s\n```\n", strings.TrimSpace(gap.CodeExcerpt))
	}

	if len(ctxBlock.Controls) > 0 {
		fmt.Fprintf(&sb, "\n## Applicable controls (snapshotted at %s)\n\n", ctxBlock.SnapshotAt.Format("2006-01-02T15:04:05Z"))
		for _, c := range ctxBlock.Controls {
			fmt.Fprintf(&sb, "- **%s** %s: %s\n", c.ControlCode, c.Name, oneLine(c.Objective))
		}
	}

	if len(ctxBlock.KnowledgeInsights) > 0 {
		fmt.Fprintf(&sb, "\n## Knowledge insights\n\n")
		for _, k := range ctxBlock.KnowledgeInsights {
			fmt.Fprintf(&sb, "- **%s**: %s\n", k.Title, oneLine(k.Body))
		}
	}

	if len(ctxBlock.ForesightChains) > 0 {
		fmt.Fprintf(&sb, "\n## Foresight (downstream effects to consider)\n\n")
		for _, f := range ctxBlock.ForesightChains {
			fmt.Fprintf(&sb, "- %s\n", strings.Join(f.Steps, " → "))
		}
	}

	if len(ctxBlock.ApplicableRisks) > 0 {
		fmt.Fprintf(&sb, "\n## In-flight risks (avoid duplicating)\n\n")
		for _, r := range ctxBlock.ApplicableRisks {
			fmt.Fprintf(&sb, "- %s [%s] %s\n", r.ID, r.ControlCode, oneLine(r.Summary))
		}
	}

	fmt.Fprintf(&sb, "\n## Constraints\n\n")
	fmt.Fprintf(&sb, "- Output a SINGLE unified diff in a fenced ```diff block.\n")
	fmt.Fprintf(&sb, "- The diff MUST modify exactly the file: %s\n", gap.FilePath)
	if g.MaxAddedLines > 0 {
		fmt.Fprintf(&sb, "- The diff MUST add at most %d lines.\n", g.MaxAddedLines)
	}
	if len(g.RequiredHints) > 0 {
		fmt.Fprintf(&sb, "- The diff MUST contain one of: %s\n", strings.Join(g.RequiredHints, " | "))
	}
	fmt.Fprintf(&sb, "- Do NOT add comments explaining the change.\n")
	fmt.Fprintf(&sb, "- Do NOT include any prose outside the fenced diff.\n")
	fmt.Fprintf(&sb, "\nProduce the diff now.\n")
	return sb.String()
}

// oneLine collapses whitespace so multi-line control text fits.
func oneLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	if len(s) > 240 {
		s = s[:237] + "..."
	}
	return s
}

// PrimaryControlCode returns the first applicable control code for a
// gap pattern. The synthesizer stamps this on each CandidatePatch so
// the verifier can correlate.
func PrimaryControlCode(ctxBlock *enrichment.IssueContextBlock) string {
	if ctxBlock == nil || len(ctxBlock.Controls) == 0 {
		return ""
	}
	return ctxBlock.Controls[0].ControlCode
}

// avoid unused import warning for polaris when controls list is empty
var _ = polaris.Control{}
