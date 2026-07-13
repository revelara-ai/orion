package conductor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/revelara-ai/orion/internal/actuation"
	"github.com/revelara-ai/orion/internal/brownfield"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/notify"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/newbehavior"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/reliabilityfloor"
	"github.com/revelara-ai/orion/internal/reliabilityscan"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
	"github.com/revelara-ai/orion/internal/worktree"
	"github.com/revelara-ai/orion/pkg/llm"
)

// ChangeResult is the outcome of a brownfield change-proof.
type ChangeResult struct {
	Branch           string
	Path             string // the worktree the change lives in
	Regression       brownfield.RegressionResult
	FilesChanged     []string
	NewBehavior      *truthalign.ModeResult // nil when no ratified cases were supplied
	Committed        bool
	Reason           string          // why not committed, if applicable
	Tier             string          // reliability tier classified from the change worktree (or-v9f.15)
	Delivery         string          // "deliver" | "escalate" — the same decision semantic as the greenfield bar
	PR               PRResult        // PR-ready handoff over the review branch on deliver (or-v9f.15)
	Landed           bool            // or-7fd: auto-landed post-proof under the standing opt-in
	ExistingArtifact bool            // or-mvs: an existing proven branch was recommended instead of re-deriving
	Alignment        AlignmentRecord // or-3p5.4: advisory intent-alignment audit of the proven change (log-only)
	EscalationID     string          // inbox escalation recorded on escalate (or-v9f.15)

	// Reliability floor (or-uvw.8, log-only): corpus-sourced signals retrieved once in
	// the trusted control plane, used twice — advisory generator context + lint checks.
	FloorSignals []reliabilityfloor.Signal
	FloorLint    reliabilityfloor.LintResult

	// Evidence (or-ykz.12): the before/after differential of the regression
	// gate's runs — reviewable proof of WHAT changed behaviorally, attached to
	// the PR artifact on deliver.
	Evidence brownfield.EvidenceDiff

	// issueID is the worktree id of THIS attempt (unexported: the retry wrapper
	// reclaims failed attempts' worktrees).
	issueID string
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
func ChangeAndProve(ctx context.Context, repoRoot string, store *contextstore.Store, provider llm.Provider, intent string, cases []newbehavior.Case, supersedes []string, sink PhaseSink) (ChangeResult, error) {
	// Operation root: one stack-wide retry budget for the whole change run
	// (or-mvr.1) — kept if a turn above already installed one.
	ctx = withLLMGuards(ctx)
	sink = syncSink(sink)
	// or-mvs: a prior run's PROVEN, still-fast-forwardable artifact beats
	// re-deriving the same change — recommend landing it (override:
	// force_rederive / ORION_REDERIVE=1).
	if !forceRederive(ctx) {
		if branch, ok := existingProvenArtifact(ctx, repoRoot, intent); ok {
			sink.emit("reuse", PhaseDone, "existing proven artifact: "+branch)
			return ChangeResult{
				Branch:           branch,
				ExistingArtifact: true,
				Reason:           reuseRecommendation(branch),
			}, nil
		}
	}
	m := brownfield.ScanRepoMap(repoRoot)
	mgr := worktree.New(repoRoot, store)

	// Reliability floor (or-uvw.8): retrieve ONCE in the trusted control plane —
	// fail-open, never blocks the change on corpus availability. Shared across
	// self-correction attempts (the corpus answer doesn't change per attempt).
	sigs := floorSignals(ctx, store, "", intent)

	// or-3p5.13: consult memory ONCE (project-scoped, best-effort) — prior
	// failures/decisions ride every attempt's generator prompt, and the
	// verdict below writes back so a re-run never re-derives.
	mem := openChangeMemory(ctx, store)
	if mem != nil {
		defer func() { _ = mem.Close() }()
	}
	memBrief := changeMemoryBrief(ctx, store, mem, intent)

	// Bounded self-correction (or-sk7u): when the regression gate or the
	// new-behavior proof rejects, re-invoke the GENERATOR with the failure
	// digest and re-prove in a fresh worktree. The oracle never changes between
	// attempts — the generator iterates, the judge doesn't (self-correction must
	// not become self-grading). Non-retryable outcomes (red button, introduced
	// secrets, an empty change) escalate immediately.
	attempts := changeAttempts()
	var digests []string
	var prevSnap refinementSnapshot // or-mvr.5
	var res, lastMeaningful ChangeResult
	var retryable bool
	var err error
	feedback := ""
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			sink.emit("self-correct", PhaseRunning, fmt.Sprintf("attempt %d/%d — feeding the failure digest back to the generator", attempt, attempts))
		}
		res, retryable, err = changeAttempt(ctx, repoRoot, store, provider, mgr, m, sigs, intent, memBrief+feedback, cases, supersedes, sink)
		if err != nil {
			return res, err // infrastructure error — retrying won't change it
		}
		res.FloorSignals = sigs
		if !res.Committed && attempt > 1 && len(res.FilesChanged) == 0 && lastMeaningful.Reason != "" {
			// The generator GAVE UP on the retry (produced nothing): the previous
			// attempt's evidence is the meaningful failure — report that, not the
			// vacuous empty attempt.
			res = lastMeaningful
			break
		}
		if res.Committed || !retryable {
			break
		}
		lastMeaningful = res
		d := res.FailureDigest()
		if d == "" {
			d = res.Reason
		}
		digests = append(digests, fmt.Sprintf("attempt %d: %s\n%s", attempt, res.Reason, d))
		// Net-negative-refinement detector (or-mvr.5): a retry that BROKE MORE
		// than the attempt it was fixing terminates self-correction now.
		cur := refinementSnapshot{NewTestFailures: len(res.Evidence.NewFailures)}
		if attempt > 1 {
			if regressed, why := refinementRegressed(prevSnap, cur); regressed {
				sink.emit("self-correct", PhaseWarn, why+" — terminating refinement")
				digests = append(digests, why)
				break
			}
		}
		prevSnap = cur
		if attempt < attempts {
			// The failed attempt's worktree is uncommitted scratch — reclaim it
			// best-effort so retries don't accumulate junk (or-kt5 tracks the
			// general cleanup).
			_ = mgr.Remove(ctx, res.issueID, worktree.RemoveOpts{Force: true})
			feedback = "\n\nPREVIOUS ATTEMPT FAILED — fix and retry. Do not repeat the same mistake. Evidence:\n" + digests[len(digests)-1]
		}
	}
	if !res.Committed && len(digests) > 0 {
		// Budget spent (or last attempt still failing): the human sees the WHOLE
		// trajectory, not just the final failure.
		res.Reason = fmt.Sprintf("%d attempt(s) failed:\n%s", len(digests), strings.Join(digests, "\n---\n"))
		res.Delivery = "escalate"
	}
	// or-qnto residual 3: a change that OVERCAME failed attempts carries the
	// same transferable signal a greenfield task does — mirror the distill
	// pass over its digest trajectory (opt-in, generation-tier candidate).
	if res.Committed && len(digests) > 0 {
		distillChangeRule(ctx, mem, intent, digests)
	}
	final := finishChange(ctx, store, repoRoot, res, intent)
	// or-kt5: a NOT-committed change reclaims its worktree AND branch now —
	// the evidence is already persisted (or-67av), so the checkout is not the
	// record. Committed changes keep both (the branch IS the deliverable).
	if !final.Committed && final.issueID != "" {
		_ = mgr.RemoveWithBranch(ctx, final.issueID, worktree.RemoveOpts{Force: true})
	}
	// or-3p5.13: WRITE the verdict back — an accepted change records its
	// outcome + extracted decisions; a failed one records the causal analysis
	// so a re-run consults instead of re-deriving. Best-effort.
	rememberChangeOutcome(ctx, mem, repoRoot, intent, final)
	return final, nil
}

// changeAttempts is the self-correction budget: total generator attempts per
// change (ORION_CHANGE_ATTEMPTS, default 3 = the initial try + 2 retries).
func changeAttempts() int {
	if v, err := strconv.Atoi(os.Getenv("ORION_CHANGE_ATTEMPTS")); err == nil && v >= 1 {
		return v
	}
	return 3
}

// changeAttempt runs ONE generate→prove pass in a fresh worktree. retryable
// reports whether a failed outcome is the generator's to fix (regression or
// new-behavior fail) vs a hard stop (red button, secrets, empty change).
func changeAttempt(ctx context.Context, repoRoot string, store *contextstore.Store, provider llm.Provider, mgr *worktree.Manager, m brownfield.RepoMap, sigs []reliabilityfloor.Signal, intent, feedback string, cases []newbehavior.Case, supersedes []string, sink PhaseSink) (ChangeResult, bool, error) {
	// Fresh, non-colliding worktree per run (or-3p5.7): re-running the same intent must
	// not collide on the slug's path/branch, and must never clobber a prior committed
	// change branch. A fresh id (suffix -2/-3 on collision) replaces the old broken
	// Create→CreateResume fallback, which couldn't recover from a pre-existing directory.
	issueID := freshChangeID(ctx, mgr, repoRoot, "orion-change-"+slugFromIntent(intent))
	sink.emit("change worktree", PhaseRunning, "")
	wt, err := mgr.Create(ctx, issueID, "HEAD")
	if err != nil {
		sink.emit("change worktree", PhaseFailed, err.Error())
		return ChangeResult{}, false, fmt.Errorf("worktree for change: %w", err)
	}
	sink.emit("change worktree", PhaseDone, wt.Branch)
	res := ChangeResult{Branch: wt.Branch, Path: wt.Path, issueID: issueID}

	// Regression gate: green-before (worktree == HEAD) → the generator edits the
	// worktree → green-after. The generator IS the change being applied. The DEFAULT is the
	// scoped gate (changed packages + blast radius; it auto-escalates to the full suite on a
	// go.mod/go.sum change and holds vacuously when no Go package is touched) — fast on big
	// repos like Orion-on-Orion. ORION_REGRESSION_SCOPE=full forces the whole suite as a
	// manual safety hatch (e.g. a build-tag/codegen change with out-of-import-graph coupling).
	// See or-3p5.5.
	apply := func() error {
		// Use TWICE, part 1: floor signals ride the repo digest as advisory generator context.
		// Use TWICE, part 1 (floor) + self-correction evidence (or-sk7u): the
		// failure digest from the prior attempt rides the same generator context.
		return DiffGenerator(ctx, provider, wt.Path, intent, m.Digest()+"\n"+reliabilityfloor.RenderContext(sigs)+feedback, supersedes)
	}
	// The gate's Progress heartbeat rides the same sink as the phase events —
	// per-package completions land in Detail, so a 10-minute suite is visibly
	// alive in the TUI activity panel and the CLI (or-m45w).
	progress := brownfield.Progress(func(step, detail string) {
		sink.emit("regression gate", PhaseRunning, step+" · "+detail)
	})
	var reg brownfield.RegressionResult
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ORION_REGRESSION_SCOPE")), "full") {
		sink.emit("regression gate", PhaseRunning, "full suite (ORION_REGRESSION_SCOPE=full)")
		reg, err = brownfield.RegressionGate(ctx, wt.Path, supersedes, apply, progress)
	} else {
		sink.emit("regression gate", PhaseRunning, "scoped: changed packages + blast radius")
		reg, err = brownfield.RegressionGateScoped(ctx, wt.Path, m, supersedes, apply, progress)
	}
	if err != nil {
		sink.emit("regression gate", PhaseFailed, err.Error())
		return res, false, fmt.Errorf("regression gate: %w", err)
	}
	res.Regression = reg
	res.Evidence = brownfield.Diff(reg)
	res.FilesChanged = changedFiles(ctx, wt.Path)

	// Use TWICE, part 2: log-only lint of the changed dirs against the mechanizable
	// signals. Never branch on it (slice 1 is a tracer; blocking arrives tier-gated later).
	res.FloorLint = runFloorChecks(ctx, wt.Path, sigs, res.FilesChanged)
	logFloor(res)

	if !reg.Held {
		sink.emit("regression gate", PhaseWarn, reg.Reason)
		res.Reason = reg.Reason
		res.Delivery = "escalate"
		return res, true, nil // the generator's to fix — retryable (or-sk7u)
	}

	// New-behavior proof (or-3p5.3): the regression gate proved do-no-harm; this proves
	// the change does what was asked, against the ratified cases (oracle = the case, never
	// the generator). Commit is gated on regression-held AND new-behavior=Accept.
	sink.emit("regression gate", PhaseDone, "do-no-harm held")

	// or-06lr: hazard-preservation gate — the ratified STAMP baseline's
	// CONTROLLED UCAs must survive the change (deterministic token-presence
	// differential: present pre-change, gone post-change = the control
	// vanished). No baseline → a VISIBLE advisory skip, never a silent pass.
	if ucas := loadControlledUCAs(ctx, store); len(ucas) == 0 {
		sink.emit("hazard gate", PhaseDone, "no ratified STAMP baseline — advisory skip (propose_stamp_baseline → ratify_stamp_baseline to arm it)")
	} else {
		violations, stale := hazardGate(ucas, repoRoot, wt.Path)
		for _, st := range stale {
			sink.emit("hazard gate", PhaseWarn, "stale baseline token (absent before this change): "+st)
		}
		if len(violations) > 0 {
			var b strings.Builder
			b.WriteString("hazard gate: controlled UCA token(s) VANISHED in this change — the control must be preserved (or the baseline re-ratified):")
			for _, v := range violations {
				b.WriteString("\n  " + v.UCAID + " (" + v.Hazard + "): token \"" + v.Token + "\" no longer present")
			}
			sink.emit("hazard gate", PhaseWarn, b.String())
			res.Reason = b.String()
			res.Delivery = "escalate"
			return res, true, nil // the generator can restore the control — retryable
		}
		sink.emit("hazard gate", PhaseDone, "controlled UCAs preserved")
	}

	if len(cases) > 0 {
		sink.emit("new-behavior proof", PhaseRunning, fmt.Sprintf("proving %d ratified case(s)", len(cases)))
		mr, nbErr := newbehavior.ProveNewBehavior(ctx, wt.Path, cases)
		if nbErr != nil {
			sink.emit("new-behavior proof", PhaseFailed, nbErr.Error())
			return res, false, fmt.Errorf("new-behavior proof: %w", nbErr)
		}
		res.NewBehavior = &mr
		if !mr.Pass {
			sink.emit("new-behavior proof", PhaseWarn, "cases did not pass")
			res.Reason = "regression held, but the new-behavior proof did not pass"
			res.Delivery = "escalate"
			return res, true, nil // the generator's to fix — retryable (or-sk7u)
		}
	}

	// or-v9f.15: the change flow gets the delivery tail's gates — previously it
	// committed with no security check and even while the red button was engaged.
	// The security gate judges the CHANGE, not the repo: only findings inside the
	// changed files block, so pre-existing debt never wedges brownfield work.
	if res.NewBehavior != nil && res.NewBehavior.Pass {
		sink.emit("new-behavior proof", PhaseDone, "all cases pass")
	}
	if findings := secretFindingsInChanged(wt.Path, res.FilesChanged); len(findings) > 0 {
		sink.emit("security gate", PhaseWarn, "hardcoded secret(s) introduced")
		res.Reason = "security gate: hardcoded secret(s) introduced by the change: " + strings.Join(findings, ", ")
		res.Delivery = "escalate"
		return res, false, nil // hard stop: never iterate toward hiding a secret better
	}
	rb := actuation.RedButton{}
	if store != nil {
		rb.Path = filepath.Join(store.Dir(), "red_button")
	}
	if gerr := rb.Guard("commit change branch"); gerr != nil {
		res.Reason = gerr.Error()
		res.Delivery = "escalate"
		return res, false, nil // hard stop: the red button is a human order, not a failure to fix
	}

	// Stage ONLY the intended change. res.FilesChanged was snapshotted right after the edit,
	// before any verifier ran — so it excludes the sandbox scratch the verify step writes into
	// the worktree (.orion-gocache/, .orion-gopath/, .config/, the curated .orion-golangci.yml).
	// A blanket `git add -A` would commit that junk; staging the snapshot keeps the commit clean.
	if len(res.FilesChanged) == 0 {
		res.Reason = "the generator produced no file changes"
		res.Delivery = "escalate"
		return res, false, nil // an empty change twice would be an empty change again
	}
	if _, err := gitIn(ctx, wt.Path, append([]string{"add", "-A", "--"}, res.FilesChanged...)...); err != nil {
		return res, false, err
	}
	sink.emit("commit", PhaseRunning, "committing to "+res.Branch)
	if _, err := gitIn(ctx, wt.Path,
		"-c", "user.name=Orion", "-c", "user.email=orion@revelara.ai", "-c", "commit.gpgsign=false",
		"commit", "--no-verify", "-m", changeMessage(intent)); err != nil {
		sink.emit("commit", PhaseFailed, err.Error())
		return res, false, err
	}
	res.Committed = true
	sink.emit("commit", PhaseDone, res.Branch)
	res.Delivery = "deliver"
	// Tier classification over the CHANGED files (a change worktree has no single
	// main.go artifact; non-Go changes classify at the base tier). Pure scan — a
	// brownfield change has no project row to record against.
	var findings []reliabilityscan.Finding
	for _, f := range res.FilesChanged {
		if !strings.HasSuffix(f, ".go") {
			continue
		}
		if b, rerr := os.ReadFile(filepath.Join(wt.Path, f)); rerr == nil { // #nosec G304 -- harness-created worktree, changed-file list from git
			findings = append(findings, reliabilityscan.ScanSource(string(b))...)
		}
	}
	res.Tier = string(reliabilitytier.Classify(reliabilityscan.DeriveDimensions(findings)))

	// or-3p5.4 residual: ADVISORY alignment gate on the proven change — the
	// same single semantic-drift detector the greenfield loop runs (V3 Step 1
	// posture: log-only, surfaces to the human, never flips a proof verdict).
	// Offline (no provider) skips silently. or-3ik fix: the judge resolves
	// through RoleProvider("align", …) EXACTLY as greenfield does
	// (oriontools.go) — an independent model when one is configured
	// (ORION_ALIGN_MODEL / ORION_MODEL_ALIGN), otherwise the same model under
	// the adversarial-auditor role, never the generator's own "generate"
	// framing grading itself (or-kzf.1). The audit always runs; it just never
	// self-grades under its own criteria.
	if provider != nil {
		// Judge ONLY the changed surface: the aligner walks every .go file
		// under the dir it is given, and a change worktree holds the WHOLE
		// repo — a scratch copy of the changed files keeps the audit scoped
		// (and the judge's window intact) on any real codebase.
		scope, serr := changedScopeDir(wt.Path, res.FilesChanged)
		if serr == nil {
			defer func() { _ = os.RemoveAll(scope) }()
		}
		aligner := NativeAligner(AlignJudgeProvider(RoleProvider("align", provider)))
		if v, aerr := aligner(ctx, intent, scope, nil); serr == nil && aerr == nil {
			res.Alignment = AlignmentRecord{Ran: true, Aligned: v.Aligned, Severity: normalizeSeverity(v.Severity), Concern: v.Concern}
			if v.Inconclusive {
				sink.emit("align", PhaseWarn, "audit inconclusive (refusal/no verdict): "+v.Concern)
			} else if !v.Aligned {
				sink.emit("align", PhaseWarn, "advisory concern ("+res.Alignment.Severity+"): "+v.Concern)
			} else {
				sink.emit("align", PhaseDone, "change serves the intent")
			}
		}
	}
	return res, false, nil
}

// FailureDigest distills the regression gate's failing run for the loop: red
// baseline digests Before, green→red digests After; a held gate has none. This
// is the evidence a model needs to SELF-CORRECT (or-67av) — "held=false" alone
// dead-ends the loop; "undefined: filepath" closes it.
func (r ChangeResult) FailureDigest() string {
	if r.Regression.Held {
		return ""
	}
	failing := r.Regression.After
	if !r.Regression.Before.Passed {
		failing = r.Regression.Before
	}
	return brownfield.FailureDigest(failing.Output, 40)
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
			// or-7fd: standing opt-in lands the proven change NOW — ff-only merge,
			// close the cited issue, reclaim the branch — no per-change prompt.
			// Red button always wins; any landing miss (stale base) falls through
			// to the normal review/PR handoff instead.
			granted, explicit := postProofAutonomy()
			if !explicit {
				granted = earnedPostProofAutonomy(ctx, store, res.Tier)
			}
			if granted {
				rb := actuation.RedButton{Path: filepath.Join(store.Dir(), "red_button")}
				if actuation.AutonomousDeliverPermitted(rb, res.Delivery) {
					summary, lerr := LandProvenChange(ctx, repoRoot, store, rb, res.Branch, intent)
					if lerr == nil {
						res.Landed = true
						_ = notify.Notify(ctx, notify.Event{
							Kind: "change.landed", Task: oneLine(intent), Verdict: "Accept",
							Detail: summary, Artifact: res.Branch, NextAction: "none — landed",
						})
						return res
					}
					res.Reason = strings.TrimSpace(res.Reason + "\nauto-land declined: " + lerr.Error())
				}
			}
			if pr, perr := ChangePRHandoff(ctx, repoRoot, store.Dir(), res.Path, res.Branch, intent, res.Tier, res.Evidence.Markdown()); perr == nil {
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
				detail := "intent: " + oneLine(intent) + "\nreview branch: " + res.Branch
				if d := res.FailureDigest(); d != "" {
					detail += "\n\ndo-no-harm transcript (digest):\n" + d
				}
				id, e := tx.Escalations().CreateDetailed(ctx, pid, "",
					"brownfield change: "+res.Reason, detail)
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
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain").Output() // #nosec G204 -- fixed binary + fixed args
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

// changedScopeDir copies the changed files into a scratch tree so the
// alignment judge audits the CHANGE, not the whole repo (or-3p5.4).
func changedScopeDir(root string, files []string) (string, error) {
	dir, err := os.MkdirTemp("", "orion-align-scope-")
	if err != nil {
		return "", err
	}
	for _, f := range files {
		b, rerr := os.ReadFile(filepath.Join(root, f)) // #nosec G304 -- harness worktree + git-derived list
		if rerr != nil {
			continue // deleted files have no content to audit
		}
		dst := filepath.Join(dir, f)
		if merr := os.MkdirAll(filepath.Dir(dst), 0o755); merr != nil {
			return dir, merr
		}
		if werr := os.WriteFile(dst, b, 0o600); werr != nil {
			return dir, werr
		}
	}
	return dir, nil
}
