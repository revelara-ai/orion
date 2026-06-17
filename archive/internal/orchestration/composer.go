package orchestration

import (
	"fmt"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/github"
	"github.com/revelara-ai/orion/internal/harness"
	"github.com/revelara-ai/orion/internal/report"
	"github.com/revelara-ai/orion/internal/verify"
)

// Compose is the v1 greedy composer. Per SPEC §12.6 / §12.7:
//
//   - Filter to accepted patches only.
//   - Sort by largest improvement (sum of decisive-axis improvements).
//   - Drop later patches whose TargetPath conflicts with an earlier
//     accepted patch (v1 simplification: one patch per file). Future
//     epics will re-verify per composition step.
//   - Render the report markdown for the composition.
//   - Return a PRPlan with one PlannedCommit per accepted patch.
func Compose(opts RunOptions, h *harness.Harness, accepted []VerifiedPatch) *PRPlan {
	if len(accepted) == 0 {
		return nil
	}

	// Sort by improvement descending; stable on (TargetPath, GapID) for
	// determinism when scores tie.
	sorted := make([]VerifiedPatch, len(accepted))
	copy(sorted, accepted)
	sort.SliceStable(sorted, func(i, j int) bool {
		si := totalImprovement(sorted[i].Verdict)
		sj := totalImprovement(sorted[j].Verdict)
		if si != sj {
			return si > sj
		}
		if sorted[i].Patch.TargetPath != sorted[j].Patch.TargetPath {
			return sorted[i].Patch.TargetPath < sorted[j].Patch.TargetPath
		}
		return sorted[i].Patch.GapID < sorted[j].Patch.GapID
	})

	seen := map[string]bool{}
	commits := make([]PlannedCommit, 0, len(sorted))
	for _, vp := range sorted {
		if seen[vp.Patch.TargetPath] {
			continue
		}
		seen[vp.Patch.TargetPath] = true
		commits = append(commits, PlannedCommit{
			CommitMessage: composeCommitMessage(vp),
			TargetPath:    vp.Patch.TargetPath,
			UnifiedDiff:   vp.Patch.UnifiedDiff,
			ControlID:     vp.Patch.ControlID,
			Patch:         vp.Patch,
			Verdict:       vp.Verdict,
		})
	}
	if len(commits) == 0 {
		return nil
	}

	branch := github.BranchName(shortRunID(opts.RunID), opts.Issue.ExternalID)
	body := report.Render(report.Data{
		RunID:                opts.RunID,
		Issue:                report.Issue{Title: opts.Issue.Title, ExternalID: opts.Issue.ExternalID},
		Harness:              h,
		AcceptedPatches:      verifiedToReport(accepted),
		BundleURLPlaceholder: fmt.Sprintf("orion-runs/%s/bundle.json (placeholder; full bundle ships in Epic 12)", opts.RunID),
	})
	return &PRPlan{
		BranchName: branch,
		Title:      composeTitle(opts.Issue.Title, commits),
		Body:       body,
		Commits:    commits,
	}
}

// totalImprovement sums the lower-bound deltas across decisive axes.
// Lower-bound is conservative: the worst the CI guarantees.
func totalImprovement(v verify.Verdict) float64 {
	total := 0.0
	for _, ax := range v.Axes {
		if ax.Decision == "decisive" {
			total += ax.DeltaCI.Lower
		}
	}
	return total
}

// composeCommitMessage follows SPEC §16.1: include the Polaris control
// ID and the verification axis improved.
func composeCommitMessage(vp VerifiedPatch) string {
	axes := make([]string, 0, len(vp.Verdict.Axes))
	for _, ax := range vp.Verdict.Axes {
		if ax.Decision == "decisive" {
			axes = append(axes, string(ax.Axis))
		}
	}
	axesStr := "no decisive axis"
	if len(axes) > 0 {
		axesStr = strings.Join(axes, ", ")
	}
	subject := fmt.Sprintf("orion(%s): apply %s remediation", vp.Patch.ControlID, vp.Patch.Pattern)
	body := fmt.Sprintf("Improves: %s\n\nGap: %s\nLLM: %s seed=%d\n", axesStr, vp.Patch.GapID, vp.Patch.LLMModel, vp.Patch.LLMSeed)
	return subject + "\n\n" + body
}

// composeTitle follows SPEC §16.1.
func composeTitle(issueTitle string, commits []PlannedCommit) string {
	controls := uniqueControls(commits)
	return fmt.Sprintf("orion: %s [%d patches across %s]", issueTitle, len(commits), strings.Join(controls, ", "))
}

func uniqueControls(commits []PlannedCommit) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(commits))
	for _, c := range commits {
		if c.ControlID == "" || seen[c.ControlID] {
			continue
		}
		seen[c.ControlID] = true
		out = append(out, c.ControlID)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return []string{"unknown-control"}
	}
	return out
}

// shortRunID returns the first 6 chars of runID (lowercased).
func shortRunID(runID string) string {
	if len(runID) > 6 {
		return strings.ToLower(runID[:6])
	}
	return strings.ToLower(runID)
}

// verifiedToReport adapts the orchestration type to the report type.
func verifiedToReport(vps []VerifiedPatch) []report.AcceptedPatch {
	out := make([]report.AcceptedPatch, 0, len(vps))
	for _, vp := range vps {
		out = append(out, report.AcceptedPatch{
			GapID:      vp.Patch.GapID,
			ControlID:  vp.Patch.ControlID,
			Pattern:    string(vp.Patch.Pattern),
			TargetPath: vp.Patch.TargetPath,
			LLMModel:   vp.Patch.LLMModel,
			LLMSeed:    vp.Patch.LLMSeed,
			Verdict:    vp.Verdict,
		})
	}
	return out
}
