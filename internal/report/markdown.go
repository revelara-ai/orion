package report

import (
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/verify"
)

// Render returns the markdown PR body. v1 sections per SPEC §16.1 +
// §12.8: title context, harness config summary, per-axis baseline /
// patched / CI bounds / decision per accepted patch, operating
// envelope, run_id link.
func Render(d Data) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Orion run %s\n\n", d.RunID)
	if d.Issue.ExternalID != "" {
		fmt.Fprintf(&sb, "**Source issue:** %s — %s\n\n", d.Issue.ExternalID, d.Issue.Title)
	} else if d.Issue.Title != "" {
		fmt.Fprintf(&sb, "**Source issue:** %s\n\n", d.Issue.Title)
	}

	if d.Harness != nil {
		fmt.Fprintf(&sb, "### Harness configuration\n\n")
		fmt.Fprintf(&sb, "- Namespace: `%s`\n", d.Harness.Namespace)
		fmt.Fprintf(&sb, "- Seed: `%d`\n", d.Harness.Seed)
		fmt.Fprintf(&sb, "- Thoroughness: `%s`\n", d.Harness.Thoroughness)
		fmt.Fprintf(&sb, "- Workload: %d endpoints, target %d RPS, %d s per trial\n",
			len(d.Harness.Workload.Endpoints), d.Harness.Workload.TargetRPS, d.Harness.Workload.DurationSeconds)
		fmt.Fprintf(&sb, "- Faults: %d profiled dependencies\n\n", len(d.Harness.Faults.Faults))
	}

	if len(d.AcceptedPatches) == 0 {
		fmt.Fprintf(&sb, "### No improvements\n\n")
		fmt.Fprintf(&sb, "Verifier accepted no candidate patches. See run record for rejected candidates and rejection classes.\n\n")
	} else {
		fmt.Fprintf(&sb, "### Accepted patches (%d)\n\n", len(d.AcceptedPatches))
		for i, ap := range d.AcceptedPatches {
			fmt.Fprintf(&sb, "#### %d. %s [`%s`] — `%s`\n\n", i+1, ap.Pattern, ap.ControlID, ap.TargetPath)
			fmt.Fprintf(&sb, "- Gap: `%s`\n", ap.GapID)
			fmt.Fprintf(&sb, "- LLM: `%s` seed=%d\n", ap.LLMModel, ap.LLMSeed)
			fmt.Fprintf(&sb, "- Trials consumed: %d / %d\n\n", ap.Verdict.PairedTrialsConsumed, ap.Verdict.MaxTrials)

			fmt.Fprintf(&sb, "| Axis | Baseline | Patched | CI Lower | CI Upper | Decision |\n")
			fmt.Fprintf(&sb, "|---|---|---|---|---|---|\n")
			for _, ax := range ap.Verdict.Axes {
				fmt.Fprintf(&sb, "| `%s` | %s | %s | %s | %s | `%s` |\n",
					ax.Axis,
					formatFloat(ax.BaselineMean),
					formatFloat(ax.PatchedMean),
					formatFloat(ax.DeltaCI.Lower),
					formatFloat(ax.DeltaCI.Upper),
					ax.Decision,
				)
			}
			fmt.Fprintln(&sb)
		}
	}

	fmt.Fprintf(&sb, "### Operating envelope\n\n")
	fmt.Fprintf(&sb, "Verification was performed against the synthesized harness above. Real production envelopes may differ; this report carries the harness seed so the run is reproducible. See SPEC §12.8 for envelope confidence semantics.\n\n")

	if d.BundleURLPlaceholder != "" {
		fmt.Fprintf(&sb, "### Reproduction\n\n")
		fmt.Fprintf(&sb, "- Bundle: `%s`\n\n", d.BundleURLPlaceholder)
	}

	return sb.String()
}

// formatFloat returns a compact-but-stable string for floats. Integer-
// valued floats render without a fractional part.
func formatFloat(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%.4g", f)
}

// avoid import-of-verify being silently dropped if Render simplifies
var _ = verify.DecisionAccepted
