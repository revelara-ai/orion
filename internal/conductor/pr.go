package conductor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/delivery"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// PRResult is the developer handoff for a completed, system-validated epic (or-tcs.7).
type PRResult struct {
	ArtifactPath string   // local pr-<slug>.md — the reviewable handoff, ALWAYS written
	Branch       string   // the feature branch the proven code lives on
	Base         string   // the branch a PR targets
	Opened       bool     // true when a real remote PR was opened
	URL          string   // PR url when Opened
	Commands     []string // when not Opened: the exact push/PR commands for the developer to run
}

// gitPREnabled reports whether the developer opted into real PR creation (ORION_GIT_PR truthy).
// It is the "when asked" signal: without it the handoff takes ZERO outward action (no push, no gh),
// honoring the conservative-git default (never push to a shared remote without authority).
func gitPREnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ORION_GIT_PR"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// deliveryBase is the branch delivery forked from (GitDeliver branches off repoRoot's HEAD), which
// a PR targets. Falls back to "main" for a detached/unborn HEAD.
func deliveryBase(ctx context.Context, repoRoot string) string {
	b, err := gitIn(ctx, repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || b == "" || b == "HEAD" {
		return "main"
	}
	return b
}

// PRHandoff produces the developer handoff for a completed, system-validated epic (or-tcs.7): it
// ALWAYS writes a local PR-body artifact (the reviewable handoff — no remote required), and opens a
// REAL pull request only when the developer opted in (ORION_GIT_PR) AND the repo has an 'origin'
// remote AND gh is on PATH. Otherwise it records the exact push/PR commands and takes no outward
// action. The integration step already produced the feature branch (GitDeliver); this is the PR
// layer over it.
func PRHandoff(ctx context.Context, repoRoot, storeDir string, d GitDelivery, es spec.ExecutableSpec, verdict truthalign.Verdict, driftLine, remainder string, rb delivery.Runbook) (PRResult, error) {
	base := deliveryBase(ctx, repoRoot)

	// diff --stat of the feature branch vs its base (best-effort — the artifact is worth writing
	// even if the diff can't be computed, e.g. a fresh repo with no base divergence).
	diffstat := ""
	if d.Path != "" {
		if out, err := gitIn(ctx, d.Path, "diff", "--stat", base); err == nil {
			diffstat = out
		}
	}

	body := prBody(es, verdict, driftLine, remainder, diffstat, rb)
	return emitPR(ctx, repoRoot, storeDir, d.Branch, base, "orion: "+strings.TrimSpace(es.Intent), serviceSlug(es), body)
}

// ChangePRHandoff is the brownfield-change PR handoff (or-v9f.15): a proven
// change already committed to a review branch becomes a PR-ready artifact,
// reusing the identical push/gh/opt-in machinery as the greenfield path. The
// body is change-shaped (intent + tier + diffstat) — a change has no
// ExecutableSpec.
func ChangePRHandoff(ctx context.Context, repoRoot, storeDir, worktree, branch, intent, tier string) (PRResult, error) {
	base := deliveryBase(ctx, repoRoot)
	diffstat := ""
	if worktree != "" {
		if out, err := gitIn(ctx, worktree, "diff", "--stat", base); err == nil {
			diffstat = out
		}
	}
	return emitPR(ctx, repoRoot, storeDir, branch, base, "orion: "+oneLine(intent), changeSlug(intent), changePRBody(intent, tier, diffstat))
}

// emitPR is the shared PR-handoff tail: it ALWAYS writes the local artifact (the
// reviewable handoff — no remote required), and opens a REAL pull request only
// when the developer opted in (ORION_GIT_PR) AND the repo has an 'origin' remote
// AND gh is on PATH. Otherwise it records the exact push/PR commands and takes
// no outward action. Greenfield and brownfield share it so their outward-action
// posture can never drift.
func emitPR(ctx context.Context, repoRoot, storeDir, branch, base, title, slug, body string) (PRResult, error) {
	res := PRResult{Branch: branch, Base: base}
	artifactPath := filepath.Join(storeDir, "pr-"+slug+".md")
	if err := os.WriteFile(artifactPath, []byte(body), 0o600); err != nil {
		return res, fmt.Errorf("write PR artifact: %w", err)
	}
	res.ArtifactPath = artifactPath

	pushCmd := fmt.Sprintf("git -C %s push -u origin %s", repoRoot, branch)
	prCmd := fmt.Sprintf("gh pr create --base %s --head %s --title %q --body-file %s", base, branch, title, artifactPath)

	// Open a real PR only with opt-in AND a remote AND gh present — else hand back the commands.
	_, remoteErr := gitIn(ctx, repoRoot, "remote", "get-url", "origin")
	_, ghErr := exec.LookPath("gh")
	if !gitPREnabled() || remoteErr != nil || ghErr != nil {
		res.Commands = []string{pushCmd, prCmd}
		return res, nil
	}

	if _, err := gitIn(ctx, repoRoot, "push", "-u", "origin", branch); err != nil {
		res.Commands = []string{pushCmd, prCmd}
		return res, fmt.Errorf("push feature branch: %w", err)
	}
	// #nosec G204 -- 'gh' is a fixed binary and every arg is derived (branch name, intent title,
	// our own artifact path); exec.Command invokes no shell, so there is no injection surface.
	cmd := exec.CommandContext(ctx, "gh", "pr", "create", "--base", base, "--head", branch, "--title", title, "--body-file", artifactPath)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		res.Commands = []string{prCmd}
		return res, fmt.Errorf("gh pr create: %w: %s", err, strings.TrimSpace(string(out)))
	}
	res.Opened = true
	res.URL = strings.TrimSpace(string(out))
	return res, nil
}

// changePRBody renders the developer-facing PR description for a proven
// brownfield change: the intent, the classified reliability tier, and the diff.
func changePRBody(intent, tier, diffstat string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", strings.TrimSpace(intent))
	b.WriteString("_Brownfield change generated and regression-proven by Orion._\n\n")
	b.WriteString("### Proof\n")
	b.WriteString("- Regression gate: existing behavior preserved (green-before → green-after)\n")
	b.WriteString("- New behavior: proven against the ratified case oracle\n")
	if strings.TrimSpace(tier) != "" {
		fmt.Fprintf(&b, "- Reliability tier: %s\n", tier)
	}
	b.WriteString("\n")
	if strings.TrimSpace(diffstat) != "" {
		b.WriteString("### Changes\n```\n")
		b.WriteString(strings.TrimRight(diffstat, "\n"))
		b.WriteString("\n```\n\n")
	}
	b.WriteString("Review the proven change and merge when satisfied.\n")
	return b.String()
}

// changeSlug is a filesystem-safe slug for the change's PR artifact filename.
func changeSlug(intent string) string {
	if s := slugFromIntent(intent); s != "" {
		return s
	}
	return "change"
}

// prBody renders the developer-facing PR description for a completed, system-validated epic —
// the reviewable handoff (or-tcs.7). It carries the epic's provenance so the developer can judge
// the change without re-deriving it: the intent, the spec anchor, the proof verdict + evidence
// classes, the SystemValidate drift/wireup re-evaluation (or-tcs.10), the diff, and the runbook.
func prBody(es spec.ExecutableSpec, verdict truthalign.Verdict, driftLine, remainder, diffstat string, rb delivery.Runbook) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", strings.TrimSpace(es.Intent))
	b.WriteString("_Generated and independently proven by Orion._\n\n")

	b.WriteString("### Spec\n")
	fmt.Fprintf(&b, "- Anchor: `%s`\n", shortHash(es.Hash))
	fmt.Fprintf(&b, "- Contract: `GET %s` · port %d · %s\n\n", es.ResponseContract.Route, es.ResponseContract.Port, es.ResponseContract.Format())

	b.WriteString("### Proof\n")
	fmt.Fprintf(&b, "- Verdict: **%s** (behavioral + empirical + hazard)\n", verdict)
	if strings.TrimSpace(driftLine) != "" {
		fmt.Fprintf(&b, "- System validation: %s\n", driftLine)
	}
	b.WriteString("\n")

	if strings.TrimSpace(remainder) != "" {
		// or-v9f.5: a PARTIAL delivery — the reviewer must see exactly what is NOT
		// in this PR and why it was escalated instead of shipped.
		b.WriteString("### Escalated remainder (NOT in this delivery)\n")
		b.WriteString(strings.TrimRight(remainder, "\n"))
		b.WriteString("\n\n")
	}

	if strings.TrimSpace(diffstat) != "" {
		b.WriteString("### Changes\n```\n")
		b.WriteString(strings.TrimRight(diffstat, "\n"))
		b.WriteString("\n```\n\n")
	}

	if len(rb.Sections) > 0 {
		b.WriteString("### Runbook\n")
		keys := make([]string, 0, len(rb.Sections))
		for k := range rb.Sections {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "- **%s:** %s\n", k, rb.Sections[k])
		}
		b.WriteString("\n")
	}

	b.WriteString("Review the proven change and merge when satisfied — the evidence above is reproducible from the spec anchor.\n")
	return b.String()
}
