package conductor

import (
	"fmt"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// SpecDocument renders a ratified ExecutableSpec as a human-readable Markdown
// document — the artifact of the grill. It is a pure projection of the anchored
// spec (intent + machine-readable contract + every decision and how it was
// resolved), so what the developer reads is exactly what was anchored and what
// the build is held to.
func SpecDocument(es spec.ExecutableSpec, ratified bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Spec — %s\n\n", oneLine(es.Intent))
	if es.Hash != "" {
		fmt.Fprintf(&b, "**Anchor:** `%s`\n\n", es.Hash)
	}

	// Assumptions resolved on the developer's behalf (fallback presets) — surfaced
	// prominently for review, since the developer did NOT explicitly decide these. The
	// developer should confirm or override them before ratifying ("understand the
	// assumptions being made on their behalf").
	if asm := specAssumptions(es); len(asm) > 0 {
		b.WriteString("## ⚠ Assumptions — resolved on your behalf (review these)\n\n")
		b.WriteString("You did not specify these; Orion applied a fallback default. Override any that are wrong before ratifying:\n\n")
		for _, a := range asm {
			fmt.Fprintf(&b, "- %s\n", a)
		}
		b.WriteString("\n")
	}

	rc := es.ResponseContract
	b.WriteString("## Response contract\n\n")
	b.WriteString("| field | value |\n|---|---|\n")
	if rc.Route != "" {
		fmt.Fprintf(&b, "| route | `GET %s` |\n", rc.Route)
	}
	if rc.Port != 0 {
		fmt.Fprintf(&b, "| port | %d |\n", rc.Port)
	}
	if rc.ContentType != "" {
		fmt.Fprintf(&b, "| content-type | `%s` |\n", rc.ContentType)
	}
	if rc.TimeZone != "" {
		fmt.Fprintf(&b, "| timezone | %s |\n", rc.TimeZone)
	}
	b.WriteString("\n")

	// Decisions, grouped by dimension, marking how each was resolved.
	if len(es.Dimensions) > 0 {
		b.WriteString("## Decisions\n\n")
		for _, dim := range es.Dimensions {
			fmt.Fprintf(&b, "**%s**\n", titleCase(string(dim.Name)))
			keys := make([]string, 0, len(dim.Values))
			for k := range dim.Values {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				kind := ""
				if dim.ValueKind == "fallback_preset" {
					kind = " _(fallback)_"
				}
				fmt.Fprintf(&b, "- %s: %s%s\n", k, dim.Values[k], kind)
			}
			b.WriteString("\n")
		}
	}

	if ratified {
		b.WriteString("_Ratified. The build is proven against this contract; a proof that disagrees with it fails._\n")
	} else {
		b.WriteString("_Preview — not yet ratified. Review the assumptions above, then ratify to anchor this contract._\n")
	}
	return b.String()
}

// specAssumptions lists the decisions resolved by a fallback default rather than an
// explicit developer answer — the assumptions made on the developer's behalf, which
// they should review before ratifying.
func specAssumptions(es spec.ExecutableSpec) []string {
	var out []string
	for _, dim := range es.Dimensions {
		if dim.ValueKind != "fallback_preset" {
			continue
		}
		keys := make([]string, 0, len(dim.Values))
		for k := range dim.Values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, fmt.Sprintf("**%s** = %s  _(%s — fallback default, you did not specify it)_", k, dim.Values[k], titleCase(string(dim.Name))))
		}
	}
	return out
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 80 {
		s = s[:77] + "…"
	}
	return s
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
