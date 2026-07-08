package conductor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/actuation"
	"github.com/revelara-ai/orion/internal/brownfield"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/pkg/llm"
	"github.com/revelara-ai/orion/internal/notify"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/newbehavior"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/reliabilityscan"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
	"github.com/revelara-ai/orion/internal/worktree"
)

// ChangeResult is the outcome of a brownfield change-proof.
type ChangeResult struct {
	Branch       string
	Path         string // the worktree the change lives in
	Regression   brownfield.RegressionResult
	FilesChanged []string
	NewBehavior  *truthalign.ModeResult // nil when no ratified cases were supplied
	Committed    bool
	Reason       string // why not committed, if applicable
	Tier         string   // reliability tier classified from the change worktree (or-v9f.15)
	Delivery     string   // "deliver" | "escalate" — the same decision semantic as the greenfield bar
	PR           PRResult // PR-ready handoff over the review branch on deliver (or-v9f.15)
	EscalationID string   // inbox escalation recorded on escalate (or-v9f.15)
}

// ChangeAndProve runs the brownfield change loop end-to-end: it creates a WORKTREE off
// HEAD (the developer's working tree is never touched), has the diff generator edit it
// to the intent, then proves the change PRESERVED existing behavior via the regression
// gate (green-before → green-after). On success the change is committed on the worktree
// branch for review/PR; on a regression it is left uncommitted with the reason.
//
// This is the "do no harm + deliver" spine of brownfield. The NEW-behavior proof
// (harness-authored obligations targeting the changed surface) and STAMP-baseline
// preservation are the next rigor layers on top of this regression-gated loop.
// supersedes names existing tests whose OLD assertions this change INTENTIONALLY voids (a
// deliberate behavior change); the regression gate skips them (so the intended change isn't
// blocked as a "regression") while every other test must still survive, and the new behavior is
// proven by the ratified cases. Empty = a pure do-no-harm change.
func ChangeAndProve(ctx context.Context, repoRoot string, store *contextstore.Store, provider llm.Provider, intent string, cases []newbehavior.Case, supersedes []string) (ChangeResult, error) {
	m := brownfield.ScanRepoMap(repoRoot)

	mgr := worktree.New(repoRoot, store)
	// Fresh, non-colliding worktree per run (or-3p5.7): re-running the same intent must
	// not collide on the slug's path/branch, and must never clobber a prior committed
	// change branch. A fresh id (suffix -2/-3 on collision) replaces the old broken
	// Create→CreateResume fallback, which couldn't recover from a pre-existing directory.
	issueID := freshChangeID(ctx, mgr, repoRoot, "orion-change-"+slugFromIntent(intent))
	wt, err := mgr.Create(ctx, issueID, "HEAD")
	if err != nil {
		return ChangeResult{}, fmt.Errorf("worktree for change: %w", err)
	}
	res := ChangeResult{Branch: wt.Branch, Path: wt.Path}

	// Regression gate: green-before (worktree == HEAD) → the generator edits the
	// worktree → green-after. The generator IS the change being applied. The DEFAULT is the
	// scoped gate (changed packages + blast radius; it auto-escalates to the full suite on a
	// go.mod/go.sum change and holds vacuously when no Go package is touched) — fast on big
	// repos like Orion-on-Orion. ORION_REGRESSION_SCOPE=full forces the whole suite as a
	// manual safety hatch (e.g. a build-tag/codegen change with out-of-import-graph coupling).
	// See or-3p5.5.
	apply := func() error {
		return DiffGenerator(ctx, provider, wt.Path, intent, m.Digest(), supersedes)
	}
	var reg brownfield.RegressionResult
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ORION_REGRESSION_SCOPE")), "full") {
		reg, err = brownfield.RegressionGate(ctx, wt.Path, supersedes, apply)
	} else {
		reg, err = brownfield.RegressionGateScoped(ctx, wt.Path, m, supersedes, apply)
	}
	if err != nil {
		return res, fmt.Errorf("regression gate: %w", err)
	}
	res.Regression = reg
	res.FilesChanged = changedFiles(ctx, wt.Path)

	if !reg.Held {
		res.Reason = reg.Reason
		res.Delivery = "escalate"
		return finishChange(ctx, store, repoRoot, res, intent), nil // did not preserve existing behavior — not committed
	}

	// New-behavior proof (or-3p5.3): the regression gate proved do-no-harm; this proves
	// the change does what was asked, against the ratified cases (oracle = the case, never
	// the generator). Commit is gated on regression-held AND new-behavior=Accept.
	if len(cases) > 0 {
		mr, nbErr := newbehavior.ProveNewBehavior(ctx, wt.Path, cases)
		if nbErr != nil {
			return res, fmt.Errorf("new-behavior proof: %w", nbErr)
		}
		res.NewBehavior = &mr
		if !mr.Pass {
			res.Reason = "regression held, but the new-behavior proof did not pass"
			res.Delivery = "escalate"
			return finishChange(ctx, store, repoRoot, res, intent), nil // did not prove the asked-for behavior — not committed
		}
	}

	// or-v9f.15: the change flow gets the delivery tail's gates — previously it
	// committed with no security check and even while the red button was engaged.
	// The security gate judges the CHANGE, not the repo: only findings inside the
	// changed files block, so pre-existing debt never wedges brownfield work.
	if findings := secretFindingsInChanged(wt.Path, res.FilesChanged); len(findings) > 0 {
		res.Reason = "security gate: hardcoded secret(s) introduced by the change: " + strings.Join(findings, ", ")
		res.Delivery = "escalate"
		return finishChange(ctx, store, repoRoot, res, intent), nil
	}
	rb := actuation.RedButton{}
	if store != nil {
		rb.Path = filepath.Join(store.Dir(), "red_button")
	}
	if gerr := rb.Guard("commit change branch"); gerr != nil {
		res.Reason = gerr.Error()
		res.Delivery = "escalate"
		return finishChange(ctx, store, repoRoot, res, intent), nil
	}

	// Stage ONLY the intended change. res.FilesChanged was snapshotted right after the edit,
	// before any verifier ran — so it excludes the sandbox scratch the verify step writes into
	// the worktree (.orion-gocache/, .orion-gopath/, .config/, the curated .orion-golangci.yml).
	// A blanket `git add -A` would commit that junk; staging the snapshot keeps the commit clean.
	if len(res.FilesChanged) == 0 {
		res.Reason = "the generator produced no file changes"
		res.Delivery = "escalate"
		return finishChange(ctx, store, repoRoot, res, intent), nil
	}
	if _, err := gitIn(ctx, wt.Path, append([]string{"add", "-A", "--"}, res.FilesChanged...)...); err != nil {
		return res, err
	}
	if _, err := gitIn(ctx, wt.Path,
		"-c", "user.name=Orion", "-c", "user.email=orion@revelara.ai", "-c", "commit.gpgsign=false",
		"commit", "--no-verify", "-m", changeMessage(intent)); err != nil {
		return res, err
	}
	res.Committed = true
	res.Delivery = "deliver"
	// Tier classification over the CHANGED files (a change worktree has no single
	// main.go artifact; non-Go changes classify at the base tier). Pure scan — a
	// brownfield change has no project row to record against.
	var findings []reliabilityscan.Finding
	for _, f := range res.FilesChanged {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		if b, rerr := os.ReadFile(filepath.Join(wt.Path, f)); rerr == nil {
			findings = append(findings, reliabilityscan.ScanSource(string(b))...)
		}
	}
	res.Tier = string(reliabilitytier.Classify(reliabilityscan.DeriveDimensions(findings)))
	return finishChange(ctx, store, repoRoot, res, intent), nil
}

// finishChange fires the out-of-band event for a SETTLED change outcome
// (or-v9f.17) — all three callers (CLI, build_change, change_repo) inherit it.
// Fire-and-forget: a delivery miss never fails the change.
func finishChange(ctx context.Context, store *contextstore.Store, repoRoot string, res ChangeResult, intent string) ChangeResult {
	kind := "change.delivered"
	if res.Delivery != "deliver" {
		kind = "change.escalated"
	}
	verdict := "Reject"
	if res.Committed {
		verdict = "Accept"
	}
	// or-v9f.15: parity with the greenfield delivery tail. On DELIVER, the review
	// branch becomes a PR-ready handoff (same push/gh machinery). On ESCALATE, a
	// row lands in the unified inbox under the reserved brownfield holder project
	// so a failed change is actionable via `orion escalations list`. Both nil-safe
	// (store may be nil; a PR/escalation miss never fails the change).
	nextAction := "git diff main.." + res.Branch
	if store != nil {
		if res.Delivery == "deliver" && res.Committed {
			if pr, perr := ChangePRHandoff(ctx, repoRoot, store.Dir(), res.Path, res.Branch, intent, res.Tier); perr == nil {
				res.PR = pr
				if pr.ArtifactPath != "" {
					nextAction = "review " + pr.ArtifactPath
				}
			}
		} else if res.Delivery == "escalate" {
			_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
				pid, e := tx.Projects().GetOrCreateReserved(ctx, contextstore.BrownfieldProjectName, "brownfield")
				if e != nil {
					return e
				}
				id, e := tx.Escalations().CreateDetailed(ctx, pid, "",
					"brownfield change: "+res.Reason,
					"intent: "+oneLine(intent)+"\nreview branch: "+res.Branch)
				if e == nil {
					res.EscalationID = id
					nextAction = "orion escalations resolve " + id
				}
				return e
			})
		}
	}
	_ = notify.Notify(ctx, notify.Event{
		Kind: kind, Task: oneLine(intent), Verdict: verdict, Detail: res.Reason,
		EscalationID: res.EscalationID, PRURL: res.PR.URL,
		Artifact: res.Branch, NextAction: nextAction,
	})
	return res
}

// secretFindingsInChanged filters the worktree's secret-scan findings to the
// files the change touched — the gate judges the change, never the repo's
// pre-existing debt (or-v9f.15).
func secretFindingsInChanged(dir string, changed []string) []string {
	set := make(map[string]bool, len(changed))
	for _, f := range changed {
		set[f] = true
	}
	var out []string
	for _, finding := range proof.SecretScan(dir) {
		file := finding
		if i := strings.LastIndex(finding, ":"); i > 0 {
			file = finding[:i]
		}
		if set[file] {
			out = append(out, finding)
		}
	}
	return out
}

// freshChangeID returns a worktree id derived from base that does not collide with an
// existing worktree directory or git branch (or-3p5.7). The base is used as-is when free;
// otherwise a numeric suffix is appended (base-2, base-3, …). This keeps each `orion
// change` run isolated and idempotent — re-running the same intent never wedges on a
// stale worktree, and a prior committed change branch is preserved (not clobbered).
func freshChangeID(ctx context.Context, mgr *worktree.Manager, repoRoot, base string) string {
	taken := func(id string) bool {
		if _, err := os.Stat(mgr.PathFor(id)); err == nil {
			return true // worktree directory already exists
		}
		if _, err := gitIn(ctx, repoRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+id); err == nil {
			return true // branch already exists
		}
		return false
	}
	if !taken(base) {
		return base
	}
	for i := 2; ; i++ {
		if id := fmt.Sprintf("%s-%d", base, i); !taken(id) {
			return id
		}
	}
}

// changedFiles returns the paths the change touched in the worktree (git status).
// Porcelain v1 lines are "XY PATH": XY is a fixed 2-col status field (often space-
// padded, e.g. " M path"), then a space, then the path at index 3. The leading column
// must NOT be trimmed — doing so shifts the offset and corrupts the path.
func changedFiles(ctx context.Context, dir string) []string {
	// Raw (non-trimming) git call: gitIn TrimSpace's its output, which would strip the
	// leading status column of the first porcelain line. Porcelain parsing needs the
	// bytes verbatim.
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 4 {
			continue
		}
		path := line[3:]
		if i := strings.Index(path, " -> "); i >= 0 { // rename: "old -> new"
			path = path[i+4:]
		}
		files = append(files, strings.Trim(path, `"`))
	}
	return files
}

func changeMessage(intent string) string {
	return fmt.Sprintf("orion: %s (regression-proven)\n\nGenerated by Orion for an existing repo; the existing test suite stayed green across the change.\n", oneLine(intent))
}

// slugFromIntent makes a short, filesystem/branch-safe slug from a change intent.
func slugFromIntent(intent string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(strings.TrimSpace(intent)) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteRune(c)
		case c == ' ' || c == '-' || c == '_':
			b.WriteByte('-')
		}
		if b.Len() >= 40 {
			break
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "change"
	}
	return s
}
