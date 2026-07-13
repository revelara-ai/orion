package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/revelara-ai/orion/internal/budget"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/revelara-ai/orion/internal/actuation"
	"github.com/revelara-ai/orion/internal/contextengine"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/delivery"
	"github.com/revelara-ai/orion/internal/lspcheck"
	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/notify"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/reliabilityscan"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
	"github.com/revelara-ai/orion/internal/repo"
	"github.com/revelara-ai/orion/internal/sandbox"
	"github.com/revelara-ai/orion/internal/worktree"
)

// Generator produces the service for a spec into buildDir. The default is the
// deterministic fixture; cmd/orion injects a vendor-agent generator when opted in.
// feedback is "" on the first attempt; on a refinement attempt it carries the
// proof's causal failure analysis so the generator FIXES rather than regenerates
// blind. A generator that ignores feedback simply re-emits the same artifact (the
// loop detects the no-change and stops).
type Generator func(ctx context.Context, gs sandbox.GenSpec, buildDir string, feedback string) (sandbox.GeneratedArtifact, error)

// maxBuildAttempts bounds the refinement loop (Manifesto: bounded iteration —
// analyze + fix + re-prove, but escalate rather than compound indefinitely).
const maxBuildAttempts = 3

// BuildResult is the outcome of building + proving a ratified spec.
type BuildResult struct {
	TaskID          string
	Verdict         string // converged proof verdict (Accept/Reject); aggregate over the DAG
	Closed          bool   // lead task closed (verification-gated done)
	Partial         bool   // or-v9f.5: the proven subset was delivered; the remainder escalated
	Tier            string
	Delivery        string // "deliver" | "escalate"
	Reason          string // escalation reason, if any
	BuildDir        string
	OutputDir       string          // where the proven code was written in the dev's repo (Accept only)
	Git             GitDelivery     // git commit/branch of the proven code (when ORION_GIT_DELIVERY)
	PR              PRResult        // feature-branch PR handoff over the delivery branch (or-tcs.7)
	Alignment       AlignmentRecord // advisory intent-alignment audit (log-only in V3 Step 1)
	Attempts        int             // build+prove attempts spent on the lead task (>=1)
	FailureAnalysis string          // causal analysis of the final non-Accept verdict, if any
	TaskResults     []taskResult    // per-task outcome across the DAG (or-tcs.1.1)
}

// BuildAndProve builds the current accepted spec and proves it. It is the one-shot
// "build to the spec" pipeline shared by `orion run` and the native Orion agent's
// build_service tool; it delegates to BuildDAG, which executes the full task DAG
// (not just the lead task). gen==nil uses the deterministic fixture; onPhase (may
// be nil) streams progress lines to the conversation. Kept as a thin wrapper so
// existing callers and tests are unchanged.
func BuildAndProve(ctx context.Context, store *contextstore.Store, gen Generator, aligner Aligner, onPhase PhaseSink, outRoot string) (BuildResult, error) {
	// Operation root: one stack-wide retry budget for the whole run (or-mvr.1) —
	// kept if a turn above already installed one.
	return BuildDAG(withLLMGuards(ctx), store, gen, aligner, onPhase, outRoot)
}

// BuildDAG decomposes the accepted spec into an Epic of Tasks and executes the
// whole dependency DAG: each task is generated, proved (behavioral + empirical +
// hazard), and verification-gated INDEPENDENTLY, in dependency order, with a task
// gated until every task it DependsOn has Accepted (no task builds on an unproven
// upstream). When the DAG completes it runs the reliability scan → tier →
// deployment bar → deliver/escalate ONCE for the Epic (Phase F1). This replaces
// the prior single-Tasks[0] build (or-tcs.1.1); the generation⊥proof wall holds
// per task. Sequential by design — clustering, worktree isolation, and bounded
// parallelism are later slices.
func BuildDAG(ctx context.Context, store *contextstore.Store, gen Generator, aligner Aligner, onPhase PhaseSink, outRoot string) (finalRes BuildResult, finalErr error) {
	if gen == nil {
		gen = func(_ context.Context, gs sandbox.GenSpec, dir, _ string) (sandbox.GeneratedArtifact, error) {
			return sandbox.GenerateTimeServiceFixture(dir, gs)
		}
	}

	c := orchestrator.NewWithStore(store)
	es, err := c.RecallSpec(ctx)
	if err != nil {
		return BuildResult{}, fmt.Errorf("no accepted spec (ratify first): %w", err)
	}

	// or-v9f.16: run/phase survivability — every phase event from here on is
	// teed into the store, so a dying terminal loses nothing and attach is a
	// store-tail. The terminal sink is preserved; persistence is best-effort.
	runProj, _, _ := store.CurrentProjectSpec(ctx)
	runID := newRunID()
	// or-v9f.28: budget ceilings evaluate PROJECT spend — seed from the
	// persisted ledger and write every attributed record through to it.
	if acct := c.Budget(); acct != nil && runProj.ID != "" {
		if tok, dol, serr := store.SumSpend(ctx, runProj.ID); serr == nil {
			acct.Seed(tok, dol)
		}
		acct.SetLedger(func(role, model string, tokens int, dollars float64) {
			_ = store.AppendSpend(context.Background(), runProj.ID, runID, role, model, tokens, dollars)
		})
	}
	termSink := onPhase
	onPhase = teeRunSink(termSink, store, runProj.ID, runID, "")
	onPhase.emit("Run", PhaseRunning, "run "+runID+" started")
	defer func() {
		if finalErr != nil {
			onPhase.emit("Run", PhaseFailed, finalErr.Error())
		} else {
			onPhase.emit("Run", PhaseDone, "run "+runID+" finished")
		}
	}()

	onPhase.emit("Decompose", PhaseRunning, "")
	pv, err := c.PlanView(ctx) // decomposes + persists on demand
	if err != nil || len(pv.Tasks) == 0 {
		onPhase.emit("Decompose", PhaseFailed, "no plan")
		return BuildResult{}, fmt.Errorf("no plan: %w", err)
	}
	onPhase.emit("Decompose", PhaseDone, fmt.Sprintf("%d task(s)", len(pv.Tasks)))

	gs := sandbox.GenSpec{
		Module:   "orion-generated/service",
		Route:    es.ResponseContract.Route,
		Port:     es.ResponseContract.Port,
		Format:   es.ResponseContract.Format(), // anchored contract is the source of truth
		TimeZone: es.ResponseContract.TimeZone,
		Cases:    es.ResponseContract.Cases, // the behavioral contract the generator builds to
	}
	// or-v9f.3: a spec whose cases are exec-shaped with no HTTP contract is a CLI
	// build — the generator gets the run()/thin-main contract and the proof
	// channels target the run entry.
	hasExecCases := false
	for _, cs := range es.ResponseContract.Cases {
		if cs.Kind == spec.KindExec {
			hasExecCases = true
			break
		}
	}
	hasUnitCases := false
	for _, cs := range es.ResponseContract.Cases {
		if cs.Kind == spec.KindUnit {
			hasUnitCases = true
			break
		}
	}
	if es.ResponseContract.Route == "" && es.ResponseContract.ContentType == "" {
		switch {
		case hasExecCases:
			gs.ProgramFamily = "cli"
			gs.EntrySymbol = "run"
		case hasUnitCases:
			gs.ProgramFamily = "library"
		}
	}

	proj, _, _ := store.CurrentProjectSpec(ctx)
	model, ok, _ := stpa.Load(ctx, store, proj.ID)
	if !ok {
		// No ratified hazard model for this project → a DOMAIN-NEUTRAL skeleton, never
		// the time-service model. Imposing time-service hazards (and their HTTP/time
		// token checks) on an arbitrary build silently rejected correct non-time
		// programs. The skeleton's control is verified by the behavioral + empirical
		// obligations; a real hazard model is ratified per project (future: wired into
		// the spec flow). The time-service example ratifies stpa.DefaultModel explicitly.
		model = stpa.SkeletonModel()
		_ = stpa.Save(ctx, store, proj.ID, model)
	}
	contract := testsynth.Contract{Route: gs.Route, Format: gs.Format, TimeZone: gs.TimeZone, Cases: es.ResponseContract.Cases, EntrySymbol: gs.Entry()}
	// Shadow gate (or-v9f.3 slice 1): exec obligations are executed and recorded
	// in every report, but the VERDICT gates on them only when
	// ORION_EXEC_CASES=required — the measured cutover flips the default once the
	// shadow criterion holds (>=50 runs, <2% infra false-Inconclusive).
	requiredIDs := es.ResponseContract.RequiredCaseIDs()
	if hasExecCases && os.Getenv("ORION_EXEC_CASES") != "required" {
		requiredIDs = es.ResponseContract.RequiredCaseIDsWhere(func(cs spec.BehavioralCase) bool { return cs.Kind != spec.KindExec })
		onPhase.emit("Decompose", PhaseWarn, fmt.Sprintf("exec-shadow: %d exec case(s) execute + record but do not yet gate the verdict (ORION_EXEC_CASES=required to gate)", len(es.ResponseContract.Cases)-len(requiredIDs)))
	}

	// Cluster coupled tasks by declared file scope (or-tcs.1.2): the schedule keeps
	// cluster members contiguous, and — below — each cluster builds in its own
	// worktree. A clustering failure is non-fatal: all tasks collapse to one cluster.
	dtasks := make([]decomposer.Task, len(pv.Tasks))
	for i, t := range pv.Tasks {
		dtasks[i] = decomposer.Task{Key: t.ID, FileScope: t.FileScope, DependsOn: t.DependsOn}
	}
	clusters, cerr := decomposer.Cluster(dtasks)
	if cerr != nil {
		onPhase.emit("Cluster", PhaseWarn, cerr.Error())
		clusters = []decomposer.TaskCluster{singleCluster(pv.Tasks)}
	} else {
		onPhase.emit("Cluster", PhaseDone, fmt.Sprintf("%d task(s) in %d cluster(s)", len(pv.Tasks), len(clusters)))
	}
	scheduleTasks := orderByClusters(pv.Tasks, clusters)

	// Build in Orion's MANAGED repo (<store.Dir()>/repo), not the developer's
	// working tree: greenfield inits it, brownfield clones the target. Nothing
	// scribbles outside it, and greenfield no longer fails for "not in a git repo".
	// Greenfield (default) inits a fresh managed repo; a brownfield target —
	// ORION_BROWNFIELD_TARGET, classified by internal/brownfield — clones the
	// existing repo so the build edits real code (or-any.8). The env flag mirrors
	// ORION_GIT_DELIVERY; a first-class conductor/CLI target input lands with the
	// assembled change-proof flow (or-3p5.4).
	managed, rerr := repo.Resolve(ctx, store, brownfieldIntake(os.Getenv("ORION_BROWNFIELD_TARGET")))
	if rerr != nil {
		return BuildResult{}, fmt.Errorf("resolve managed repo: %w", rerr)
	}
	wtMgr := worktree.New(managed.Path, store).WithBase(managed.Base)
	clusterWT, getClusterWT, cleanupWT := lazyWorktrees(ctx, wtMgr, managed.Base)
	defer cleanupWT()
	clusterByTask := map[string]string{}
	for _, cl := range clusters {
		for _, m := range cl.Members {
			clusterByTask[m] = cl.Key
		}
	}

	// Execute the DAG: each task built+proved+gated INDEPENDENTLY, in dependency order
	// (a task waits until its dependencies Accept), inside its cluster's worktree. The
	// expensive proof is memoized by artifact content hash, so identical artifacts are
	// proven once (deterministic) — the scheduler enforces ordering+gating without
	// re-proving N times, even though each cluster generates into its own worktree.
	// or-b73: wire the context engine + tiered memory into the build loop so recalled
	// context (and the generation-tier poisoning quarantine) primes generation. Best-
	// effort: if memory is unavailable the loop runs with spec-only context.
	var eng *contextengine.Engine
	var mem *memory.Store
	if memDir := filepath.Join(store.Dir(), "memory"); os.MkdirAll(memDir, 0o700) == nil {
		if m, merr := memory.Open(memDir); merr == nil {
			mem = m
			// or-hd3.7: opt-in semantic recall — wire an embedder from env (default off →
			// keyword+heat recall, no model file needed).
			if e, ok := embedderFromEnv(); ok {
				mem.SetEmbedder(e)
			}
			// or-7et.3: recalled cognition is budgeted by TOKENS against the
			// active provider's window (set at the tool seam; default window
			// otherwise) — an eighth of the window, leaving the bulk for the
			// spec slice, tool results, and the response.
			eng = contextengine.New(store, mem).WithTokenBudget(generationWindow() / 8)
			defer func() { _ = mem.Close() }()
		}
	}

	// or-tcs.1.4: independent clusters build CONCURRENTLY (bounded by ORION_MAX_AGENTS, default 3),
	// each in its own worktree; the shared store/memory writes serialize on stateMu and the phase
	// sink is made concurrency-safe, so the only parallelism is the expensive generate+prove work.
	maxConc := maxAgentsFromEnv()
	safeSink := syncSink(termSink) // terminal only; the per-task tee below adds run persistence WITH task attribution
	var stateMu sync.Mutex
	// or-v9f.14: the red button is the deterministic actuation gate — consulted
	// before every cluster dispatch (in-flight clusters finish; nothing new starts)
	// and before every outward write below. File-backed, so `orion redbutton
	// engage` halts a run from any terminal.
	rb := actuation.RedButton{Path: filepath.Join(store.Dir(), "red_button")}
	// or-v9f.9: continuous drift monitoring — the spec anchor is re-verified and
	// the alignment-degradation threshold enforced before EVERY cluster dispatch,
	// not once at end-of-run.
	drift := newDriftMonitor(c)
	leaseByCluster := map[string][]string{}
	clusterMembers := map[string][]string{}
	for _, cl := range clusters {
		leaseByCluster[cl.Key] = clusterLeaseScope(cl)
		clusterMembers[cl.Key] = cl.Members
	}
	// or-v9f.26: milestone checkpoints — the proactive trajectory digest every
	// k completed clusters (advisory by default; pause-for-ack gates dispatch).
	cp := newCheckpointer(store, &stateMu, runProj.ID, runID, onPhase, requiredIDs, clusterMembers, drift.Count)
	results, err := runClusterDAG(clusters, scheduleTasks, maxConc, func(task orchestrator.PlanTask, cache map[string]proof.Report) (taskResult, error) {
		// or-7et.4c: the cluster worktree is created AT DISPATCH — an N-cluster
		// plan holds ~maxConc checkouts, not N, and integrated waves free theirs.
		buildDir, wterr := getClusterWT(clusterByTask[task.ID])
		if wterr != nil {
			return taskResult{}, fmt.Errorf("worktree for task %s: %w", task.ID, wterr)
		}
		// or-tcs.11: the generator KNOWS its lease — write tools refuse
		// out-of-scope paths, the post-generate gate diffs observed writes.
		gsTask := gs
		gsTask.Lease = leaseByCluster[clusterByTask[task.ID]]
		tr, terr := buildOneTask(ctx, store, gen, aligner, teeRunSink(safeSink, store, runProj.ID, runID, task.ID), es, model, gsTask, contract, requiredIDs, buildDir, cache, eng, mem, &stateMu, task)
		if terr == nil {
			drift.RecordAlignment(tr.Alignment)
			cp.taskCompleted(ctx, task.ID, tr.Report) // or-v9f.26 milestone cadence
		}
		return tr, terr
	}, func(clusterKey string) error {
		if err := rb.Guard("dispatch cluster " + clusterKey); err != nil {
			return err
		}
		if err := budgetGate(c.Budget()); err != nil {
			return err
		}
		// or-v9f.26 pause-for-ack: an unanswered checkpoint refuses dispatch.
		if err := cp.preDispatch(ctx); err != nil {
			return err
		}
		return drift.Check(ctx)
	})
	if err != nil {
		return BuildResult{}, err
	}

	// or-tcs.1.6: assemble the proven clusters. Each accepted cluster integrates ONE AT A TIME
	// onto a fresh epic head, re-proving the merged tree (proof.ProveAll), so the DELIVERED
	// artifact is the integrated whole — not one cluster's tree. A merge conflict or a red
	// post-merge re-proof fails the epic. The Epic is Accept only if every task Accepted AND the
	// assembly held.
	// The reprove closure runs on each assembled head (serialized by the integration queue), so the
	// LAST run leaves the proof of the fully assembled tree — capture it so the drift check re-evaluates
	// coverage against the delivered WHOLE, not one cluster's slice.
	var assembledReport proof.Report
	var haveAssembled bool
	reprove := epicReprover(store, contract, model, es, requiredIDs, proj.ID, onPhase, func(r proof.Report) {
		assembledReport, haveAssembled = r, true
	})
	conform := conformanceGate(ctx, store, proj.ID, pv.Tasks, onPhase)
	observed := observedScopeFor(ctx, store, proj.ID)
	intDir, integrated, ierr := integrateEpic(ctx, wtMgr, clusters, clusterWT, results, managed.Base, reprove, scopedWaveReprove(reprove), conform, observed, onPhase)
	if ierr != nil {
		return BuildResult{}, fmt.Errorf("epic integration: %w", ierr)
	}
	buildDir := intDir // the INTEGRATED tree is the single delivery artifact
	// Representative task result (report fields + the assembled proof the drift check re-evaluates).
	rep := results[0]
	for _, r := range results {
		if r.Verdict == "Accept" {
			rep = r
			break
		}
	}
	taskID := rep.TaskID

	// SYSTEM VALIDATION — re-evaluate the ASSEMBLED tree against the spec artifact.
	//  - or-tcs.3 (wireup GATE): every package must be reachable from a main; an orphan (built but
	//    unwired — the "6 orphan packages" lesson) REJECTS the epic even if every task Accepted.
	//  - or-tcs.10 (drift REPORT): surface the spec↔build alignment (coverage + wireup) so drift is
	//    visible to the developer, citing the artifact — the structured hook the scope-creep check
	//    extends once builds produce distinct modules.
	wired := true
	var driftLine string // the SystemValidate re-evaluation, cited in the PR handoff (or-tcs.7)
	if integrated {
		var orphans []string
		wired, orphans = systemWireupGate(intDir)
		// Judge coverage against the assembled proof when the integrator re-proved it; for a
		// single-cluster epic the integrator fast-forwards (no reprove) and the head is byte-identical
		// to the already-proven cluster, so rep.Report is the assembled proof.
		driftProof := rep.Report
		if haveAssembled {
			driftProof = assembledReport
		}
		dr, drift := driftReport(es, driftProof, orphans)
		driftLine = dr
		status := PhaseDone
		if drift {
			status = PhaseWarn
		}
		onPhase.emit("SystemValidate", status, dr)
	}

	// or-v9f.5: the epic verdict stays honest (any failed task rejects it), but a
	// proven, dependency-complete, WIRED subset is still deliverable — one failed
	// task no longer suppresses its proven siblings. The bar and the PR speak for
	// the artifact actually shipped (barVerdict); the remainder is escalated.
	outcome := evaluateEpicOutcome(results, integrated, wired)
	aggregateVerdict := outcome.aggregate
	remainder := escalatedRemainder(results)

	// or-xe7.4: pull the project's reliability context (controls/knowledge/risks) from revelara.ai
	// (MCP) before the bar — cached for reuse, and its reduced flag recorded in the envelope so a
	// delivery decided WITHOUT live reliability context is honestly marked. The touchpoint is optional
	// (no credential / unreachable → reduced, the loop proceeds).
	relctx := loadReliabilityContext(ctx, store, proj.ID, es.Intent)
	if relctx.Reduced {
		onPhase.emit("ReliabilityContext", PhaseWarn, "reduced — revelara.ai unreachable; proceeding on cached/empty reliability context")
	} else {
		onPhase.emit("ReliabilityContext", PhaseDone, "controls, knowledge, and risks pulled from revelara.ai")
	}

	// Reliability scan → tier → deployment bar → deliver or escalate (Epic-level, once).
	findings, _ := reliabilityscan.ScanAndRecord(ctx, store, proj.ID, buildDir)
	tier := reliabilitytier.Classify(reliabilityscan.DeriveDimensions(findings))
	env := delivery.OperatingEnvelope{
		ProvenLoad:                provenLoad(es),
		FaultClassesControlled:    faultClasses(model),
		Assumptions:               assumptions(model),
		ReducedReliabilityContext: relctx.Reduced,
	}
	securityOK := proof.SecurityClean(buildDir)
	// or-v9f.13: the bar is told which modes actually RAN — the assembled proof
	// when the integrator re-proved, else the representative task's — so
	// RequireAllModes is a real gate, not a hardcoded formality.
	barProof := rep.Report
	if haveAssembled {
		barProof = assembledReport
	}
	// or-v9f.12: the runbook is generated ONCE and VERIFIED against the artifact
	// before the bar — unevidenced operability claims carry UNVERIFIED markers
	// (visible in the PR) and, at Critical tier, refuse delivery.
	runbook := delivery.GenerateRunbook(es, model, env)
	var missingOps []string
	if b, rerr := os.ReadFile(filepath.Join(buildDir, "main.go")); rerr == nil {
		runbook, missingOps = delivery.VerifyRunbook(runbook, string(b))
	}
	res := delivery.EvaluateBar(outcome.barVerdict, barProof.PresentModes(), reliabilitytier.PolicyFor(tier), env, securityOK, missingOps)
	// Red Button (or-utm): while engaged, autonomy is revoked — never auto-deliver.
	if res.Decision == delivery.Deliver && rb.AutonomyRevoked() {
		res = delivery.Result{Decision: delivery.Escalate, Reason: "red button engaged: autonomy revoked, human delivery required"}
	}
	if res.Decision == delivery.Deliver {
		envJSON, _ := json.Marshal(res.Envelope)
		rbJSON, _ := json.Marshal(runbook)
		_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
			epic, e := tx.Epics().LatestForProject(ctx, proj.ID)
			if e != nil {
				return e
			}
			if _, e = tx.Deliveries().Create(ctx, epic.ID, string(envJSON), string(rbJSON)); e != nil {
				return e
			}
			if outcome.partial {
				// or-v9f.5: a PARTIAL delivery leaves the project active — the
				// escalated remainder is still its work; the queue does not advance.
				return nil
			}
			// or-v9f.1: a delivered project leaves the active slot so the next
			// queued intent can start; recorded in the same tx as the delivery.
			return tx.Projects().SetStatus(ctx, proj.ID, "delivered")
		})
		if outcome.partial {
			onPhase.emit("Deliver", PhaseWarn, "PARTIAL delivery — escalated remainder:\n"+remainder)
		} else if next, promoted, perr := store.ActivateNextQueued(ctx); perr == nil && promoted {
			onPhase.emit("Queue", PhaseDone, fmt.Sprintf("next intent activated: %s", next.Intent))
		}
	}
	if res.Decision != delivery.Deliver || outcome.partial {
		// or-v9f.4: one escalation per FAILING task, each carrying its causal
		// analysis as the decision payload — never attributed to the
		// representative task, which is by construction an ACCEPTED one. With
		// every task green (bar/security/red-button escalations) the row is
		// project-level (empty task_id) rather than misattributed. A partial
		// delivery (or-v9f.5) escalates its remainder the same way.
		reason := res.Reason
		if outcome.partial && reason == "" {
			reason = "partial delivery: task did not prove; proven siblings shipped"
		}
		_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
			failed := 0
			for _, r := range results {
				if r.Blocked || r.Verdict == "Accept" {
					continue
				}
				failed++
				// Exhaustion time (or-v9f.6) already filed this task's escalation
				// in most cases — one task, one open row; the mid-run reason wins.
				if has, e := tx.Escalations().HasOpenForTask(ctx, proj.ID, r.TaskID); e != nil {
					return e
				} else if has {
					continue
				}
				if _, e := tx.Escalations().CreateDetailed(ctx, proj.ID, r.TaskID, reason, r.FailureAnalysis); e != nil {
					return e
				}
			}
			if failed == 0 {
				_, e := tx.Escalations().CreateDetailed(ctx, proj.ID, "", reason, driftLine)
				return e
			}
			return nil
		})
	}
	deliverStatus := PhaseDone
	deliverDetail := "tier " + string(tier)
	if res.Decision != delivery.Deliver {
		deliverStatus = PhaseWarn
		deliverDetail = "escalate: " + res.Reason
	}
	onPhase.emit("Deliver", deliverStatus, deliverDetail)

	// Export: when the Epic is ACCEPTED (proven), write the code into the developer's
	// working repo so they can see + use it — not just leave it in the store's build
	// dir. Export failure is non-fatal: the proven code still lives in buildDir + the
	// store, so we warn and carry on rather than fail an otherwise-green build.
	var outputDir string
	var gitDelivery GitDelivery
	var prResult PRResult
	if gerr := rb.Guard("export proven code"); gerr != nil && outcome.barVerdict == truthalign.Accept && outRoot != "" {
		// or-v9f.14: the export + git delivery write into the DEVELOPER'S repo —
		// exactly the outward actuation the red button exists to halt. The proven
		// code stays in the build dir + store; nothing is lost, only withheld.
		onPhase.emit("Deliver", PhaseWarn, gerr.Error())
	} else if outcome.barVerdict == truthalign.Accept && outRoot != "" {
		dest := ServiceOutputDir(outRoot, es)
		if files, eerr := ExportProvenCode(buildDir, dest, es); eerr != nil {
			onPhase.emit("Deliver", PhaseWarn, "code proven but export failed: "+eerr.Error())
		} else {
			outputDir = dest
			onPhase.emit("Deliver", PhaseDone, fmt.Sprintf("code written to %s (%d files)", dest, len(files)))
		}
		// Opt-in (ORION_GIT_DELIVERY): commit the proven code onto an Orion branch in a
		// WORKTREE of the developer's repo — their working tree is untouched. Non-fatal:
		// a delivery failure warns; the code still lives in outputDir + the build dir.
		if GitDeliverEnabled() {
			if root := GitRoot(ctx, "."); root != "" {
				if d, gerr := GitDeliver(ctx, root, store, buildDir, es); gerr != nil {
					onPhase.emit("Deliver", PhaseWarn, "git delivery failed: "+gerr.Error())
				} else {
					gitDelivery = d
					onPhase.emit("Deliver", PhaseDone, fmt.Sprintf("committed to branch %s (%s)", d.Branch, d.Commit))
					// or-tcs.7: PR handoff over the feature branch — but ONLY when the epic actually
					// cleared the delivery bar. A proven-but-ESCALATED epic (e.g. the security gate
					// caught a secret, or the red button is engaged) keeps its review branch but is not
					// presented as PR-ready — and, crucially, is never auto-pushed/PR'd under opt-in.
					// Local review artifact always; a real PR only when opted in + remote + gh exist.
					if res.Decision == delivery.Deliver {
						if pr, perr := PRHandoff(ctx, root, store.Dir(), d, es, outcome.barVerdict, driftLine, remainder, runbook); perr != nil {
							prResult = pr // still carries the artifact + commands even if push/gh failed
							onPhase.emit("Deliver", PhaseWarn, "PR handoff: "+perr.Error())
						} else {
							prResult = pr
							if pr.Opened {
								onPhase.emit("Deliver", PhaseDone, "PR opened: "+pr.URL)
							} else {
								onPhase.emit("Deliver", PhaseDone, fmt.Sprintf("PR-ready: branch %s + %s", pr.Branch, pr.ArtifactPath))
							}
						}
					}
				}
			} else {
				onPhase.emit("Deliver", PhaseWarn, "git delivery requested but the working directory is not a git repo")
			}
		}
	}

	// or-v9f.17: ONE end-of-run event from the pipeline itself, so every entry
	// point (CLI run, TUI build_service, headless) inherits out-of-band
	// visibility. Fire-and-forget; the payload carries what a 3 a.m. human acts on.
	kind := "delivered"
	nextAction := ""
	switch {
	case outcome.partial:
		kind, nextAction = "partial", "orion escalations list"
	case res.Decision != delivery.Deliver:
		kind, nextAction = "escalated", "orion escalations list"
	}
	notifyArtifact := prResult.ArtifactPath
	if notifyArtifact == "" {
		notifyArtifact = outputDir
	}
	_ = notify.Notify(ctx, notify.Event{
		Kind: kind, Task: taskID, Verdict: string(aggregateVerdict), Detail: res.Reason,
		PRURL: prResult.URL, Artifact: notifyArtifact, NextAction: nextAction,
	})

	return BuildResult{
		TaskID: taskID, Verdict: string(aggregateVerdict), Closed: rep.Closed,
		Partial:   outcome.partial,
		OutputDir: outputDir,
		Tier:      string(tier), Delivery: string(res.Decision), Reason: res.Reason, BuildDir: buildDir,
		Git:       gitDelivery,
		PR:        prResult,
		Alignment: rep.Alignment, Attempts: rep.Attempts, FailureAnalysis: rep.FailureAnalysis,
		TaskResults: results,
	}, nil
}

// buildOneTask runs the bounded refinement loop for a single DAG task — generate →
// prove (behavioral + empirical + hazard) → causal-analyze + refine → verification-
// gate — into the task's own build dir. Each task is proven INDEPENDENTLY (the
// generation⊥proof wall holds per node); the harness-authored corpus is never
// readable to the generator. Returns the per-task outcome.
// stateMu (may be nil for the sequential path) serializes the SHARED-STATE touchpoints — the
// context store + memory subsystem (Dolt is single-writer; neither has an internal lock) — so
// clusters can build in parallel (or-tcs.1.4) while their store/memory writes stay race-free. The
// expensive work (generation + proof.ProveAll, in the cluster's own worktree) runs OUTSIDE it.
func buildOneTask(ctx context.Context, store *contextstore.Store, gen Generator, aligner Aligner, onPhase PhaseSink, es spec.ExecutableSpec, model stpa.Model, gs sandbox.GenSpec, contract testsynth.Contract, requiredIDs []string, buildDir string, proofCache map[string]proof.Report, eng *contextengine.Engine, mem *memory.Store, stateMu *sync.Mutex, task orchestrator.PlanTask) (taskResult, error) {
	taskID := task.ID
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return taskResult{}, fmt.Errorf("build dir: %w", err)
	}

	// or-b73: assemble the trust-tiered recalled context (spec constraints + retrieved
	// memory, generation-tier memory quarantined as data-only) and prime the generator
	// with it. Best-effort — a memory/recall miss simply yields spec-only context.
	if eng != nil {
		withLock(stateMu, func() {
			if bundle, aerr := eng.Assemble(ctx, taskID, es.Intent); aerr == nil {
				// or-7et.5c: each direct dependency's EXTRACTED surface rides the
				// always-injected constraints — deterministic lookup by task key,
				// never heat-ranked recall (which evicts at 100-module scale).
				if proj, _, perr := store.CurrentProjectSpec(ctx); perr == nil {
					injectDepSurfaces(ctx, store, proj.ID, task.DependsOn, &bundle)
				}
				gs.Context = bundle.Render(contextengine.DomainGeneration)
			}
		})
	}

	// Surface discovered skills (user-scope + self-evolved) to the generator as available
	// capabilities, neutralized against injection (skill descriptions may be untrusted). The
	// generator can read a skill's file to activate it (agentskills.io progressive disclosure).
	if cat := skillCatalogForGen(store); cat != "" {
		if gs.Context != "" {
			gs.Context += "\n\n"
		}
		gs.Context += cat
	}

	// or-gb1.3 CONSULT: known failure modes ride every generation context —
	// "never silently repeat a known failure" only works if the knowledge is
	// READ. Harness-derived (TrustProof posture); best-effort.
	if fmSection := knownFailureModesSection(ctx, store, stateMu); fmSection != "" {
		if gs.Context != "" {
			gs.Context += "\n\n"
		}
		gs.Context += fmSection
	}

	// Bounded refinement loop (Manifesto): generate → prove → if the verdict is not
	// Accept, run a CAUSAL analysis of the failing cases + feed it back so the next
	// attempt FIXES the specific failures — repeating until Accept or the attempt
	// budget is spent (then escalate, never assert success). A generator that can't
	// change its output in response to the analysis is detected (identical artifact)
	// and the loop stops early rather than burning the budget on a no-op.
	var report proof.Report
	var failureAnalysis string
	var feedback string
	attempts := 0
	var prevSnapshot refinementSnapshot // or-mvr.5: prior attempt's quality/security signals
	traj := &buildTrajectory{}          // or-gb1.4: the harness-derived refinement story
	var lastHash string
	var lastNarrative string // the latest attempt's agent self-report (or-7mr), quarantined on failure
	for attempt := 1; attempt <= maxBuildAttempts; attempt++ {
		genDetail := ""
		if attempt > 1 {
			genDetail = fmt.Sprintf("attempt %d/%d — refining from analysis", attempt, maxBuildAttempts)
		}
		onPhase.emit("Generate", PhaseRunning, genDetail)
		art, gerr := gen(ctx, gs, buildDir, feedback)
		if gerr != nil {
			onPhase.emit("Generate", PhaseFailed, gerr.Error())
			return taskResult{}, fmt.Errorf("generate: %w", gerr)
		}
		if attempt > 1 && art.ContentHash == lastHash {
			// The generator re-emitted an identical artifact despite the analysis — it
			// cannot refine further; stop and let the prior verdict escalate.
			onPhase.emit("Generate", PhaseWarn, "no change from the prior attempt — cannot refine further")
			break
		}
		lastHash = art.ContentHash
		lastNarrative = art.Narrative
		// or-tcs.11 (deterministic, covers ACP too): observed writes must fall
		// inside the cluster's lease — an out-of-scope artifact is routed to
		// refinement with the violating paths named, Rejected on repeat.
		if bad := outOfLease(gs.Lease, observedWrites(ctx, buildDir)); len(bad) > 0 {
			if scopeLeaseEnforced() {
				failureAnalysis = fmt.Sprintf("the artifact writes OUTSIDE the module's declared file scope %v: %s — regenerate strictly within your scope (delete the out-of-scope files)", gs.Lease, strings.Join(bad, ", "))
				feedback = failureAnalysis
				traj.recordFailure(failureAnalysis, buildDir)
				report = proof.Report{}
				report.Outcome.Verdict = truthalign.Reject
				onPhase.emit("Generate", PhaseWarn, "out-of-scope writes: "+strings.Join(bad, ", "))
				if attempt < maxBuildAttempts {
					continue
				}
				break
			}
			// Observe mode (default): surface the declaration/reality gap; the
			// observed-scope record grounds the integration leases regardless.
			onPhase.emit("Generate", PhaseWarn, "out-of-scope writes (advisory — ORION_SCOPE_LEASE=enforce to gate): "+strings.Join(bad, ", "))
		}
		if _, perr := sandbox.PersistArtifact(ctx, store, taskID, art); perr != nil {
			return taskResult{}, fmt.Errorf("persist artifact: %w", perr)
		}
		onPhase.emit("Generate", PhaseDone, genDetail)
		attempts = attempt

		// or-ykz.11: cheap pre-proof LSP diagnostics. A generated file with a type/compile
		// error is caught here (gopls) and fed back to the generator BEFORE the expensive
		// behavioral/empirical/hazard harness runs — cutting wasted proof passes. Skipped
		// (no-op) if gopls is absent; the proof harness compiles anyway and stays the sole
		// right-to-ship authority, so this only ever short-circuits a doomed attempt early.
		if diag, derr := lspcheck.Diagnose(ctx, buildDir); derr == nil && !diag.OK() {
			onPhase.emit("Diagnose", PhaseWarn, fmt.Sprintf("%d diagnostic(s) — refining before proof", len(diag.Diagnostics)))
			failureAnalysis = diag.Feedback()
			feedback = failureAnalysis
			traj.recordFailure(failureAnalysis, buildDir) // or-gb1.4
			report = proof.Report{}
			report.Outcome.Verdict = truthalign.Inconclusive
			if attempt < maxBuildAttempts {
				continue
			}
			break
		}

		// Proof is memoized by artifact content hash: an identical artifact (e.g. a
		// sibling cluster building the same code) is proven once — the verdict is
		// deterministic — so the DAG never re-proves the same bytes N times. The memo
		// is two-tiered: the per-run in-memory proofCache, then the CROSS-RUN
		// persisted memo keyed by (spec anchor, artifact hash) (or-v9f.6), so a
		// re-run after fixing an escalation skips proof for every unchanged cluster.
		var rep proof.Report
		// or-7et.2 slice 2: a task whose covered spec surface changed in a
		// reconciled amendment must be FRESHLY proven — the memo cannot vouch for
		// obligations that no longer mean the same thing, even on identical bytes.
		if cached, ok := proofCache[art.ContentHash]; ok && !task.ReproofRequired {
			rep = cached
			onPhase.emit("Prove", PhaseDone, "reused (identical artifact already proven)")
		} else if r, ok := recallProofMemo(ctx, store, stateMu, es.Hash, art.ContentHash); ok && !task.ReproofRequired {
			proofCache[art.ContentHash] = r
			rep = r
			onPhase.emit("Prove", PhaseDone, "reused (persisted proof — unchanged since a prior run)")
		} else {
			onPhase.emit("Prove", PhaseRunning, "behavioral + empirical + hazard")
			r, rerr := proof.ProveAllWithThreshold(ctx, buildDir, contract, model, mutationThresholdFor(buildDir))
			if rerr != nil {
				onPhase.emit("Prove", PhaseFailed, rerr.Error())
				return taskResult{}, fmt.Errorf("proof: %w", rerr)
			}
			// Coverage gate: every requirement the spec declares must have an executed,
			// passing obligation — else downgrade the verdict (the or-y9d kill).
			proof.EnforceObligations(requiredIDs, &r)
			proofCache[art.ContentHash] = r
			persistProofMemo(ctx, store, stateMu, es.Hash, art.ContentHash, r) // best-effort cross-run memo
			rep = r
			if task.ReproofRequired {
				// The fresh proof settles the amendment debt — clear the mark so
				// later runs regain memo reuse. Best-effort (the mark only ever
				// forces EXTRA proving).
				withLock(stateMu, func() {
					_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
						return tx.Tasks().SetReproofRequired(ctx, task.ID, false)
					})
				})
			}
		}
		report = rep

		if string(report.Outcome.Verdict) == "Accept" {
			d := "Accept"
			if attempt > 1 {
				d = fmt.Sprintf("Accept (attempt %d)", attempt)
			}
			onPhase.emit("Prove", PhaseDone, d)
			failureAnalysis = ""
			traj.finish(buildDir) // or-gb1.4: the passing attempt closes the trajectory
			break
		}

		// Reject/Inconclusive → causal analysis becomes the next attempt's feedback.
		failureAnalysis = analyzeFailure(report, es.ResponseContract.Cases)
		feedback = failureAnalysis
		traj.recordFailure(failureAnalysis, buildDir) // or-gb1.4

		// Net-negative-refinement detector (or-mvr.5): a pass that made the
		// artifact WORSE than the attempt it was refining terminates the loop —
		// "keep prompting until it works" is not a control strategy.
		passing, has := obligationSnapshot(report)
		cur := refinementSnapshot{PassingObligations: passing, hasObligations: has, ScanFindings: artifactScanFindings(buildDir)}
		if attempt > 1 {
			if regressed, why := refinementRegressed(prevSnapshot, cur); regressed {
				failureAnalysis = why + "\n\n" + failureAnalysis
				onPhase.emit("Prove", PhaseWarn, why+" — terminating refinement")
				break
			}
		}
		prevSnapshot = cur

		if attempt < maxBuildAttempts {
			onPhase.emit("Prove", PhaseWarn, fmt.Sprintf("%s (attempt %d/%d) — analyzing failure, refining", report.Outcome.Verdict, attempt, maxBuildAttempts))
		} else {
			onPhase.emit("Prove", PhaseWarn, fmt.Sprintf("%s after %d attempts — escalating", report.Outcome.Verdict, maxBuildAttempts))
		}
	}

	// AlignmentGate (or-809): whether the built code serves the INTENT, not just
	// the cases. I3 — it can only ever REMOVE a green light: the aligner is CALLED
	// ONLY when proof already Accepted (G1), so a Reject→Accept path is structurally
	// unreachable. Default (ORION_ALIGN_GATE unset) is LOG-ONLY (V3 Step 1 behavior,
	// byte-identical). With =block it is severity-tiered (V3 Step 3): a HIGH concern
	// blocks ONLY when a second pass corroborates it (else downgraded to medium — a
	// non-deterministic judge must not flaky-block, G5); a corroborated high
	// downgrades the converged verdict to Inconclusive with an alignment dissent
	// (removing the green light, never adding one) so the existing escalation path
	// fires; medium (incl. downgraded high) files a surface-to-human align-review
	// row but still ships (proof is the right-to-ship; never auto-fail an ambiguous
	// under-specified spec).
	var alignment AlignmentRecord
	blockGate := os.Getenv("ORION_ALIGN_GATE") == "block"
	if aligner != nil && string(report.Outcome.Verdict) == "Accept" {
		onPhase.emit("Align", PhaseRunning, "")
		if v, aerr := aligner(ctx, es.Intent, buildDir, es.ResponseContract.Cases); aerr == nil {
			sev := normalizeSeverity(v.Severity) // tolerate LLM casing/whitespace variants
			if blockGate && !v.Aligned && sev == "high" {
				// G5 corroboration: a second pass must agree, else downgrade to medium
				// (a non-deterministic judge must not flaky-block).
				if v2, e2 := aligner(ctx, es.Intent, buildDir, es.ResponseContract.Cases); e2 != nil || v2.Aligned || normalizeSeverity(v2.Severity) != "high" {
					sev = "medium"
				}
			}
			// Record the EFFECTIVE severity (post-corroboration), not the raw draw.
			alignment = AlignmentRecord{Ran: true, Aligned: v.Aligned, Severity: sev, Concern: v.Concern}
			switch {
			case v.Inconclusive:
				// or-mvr.15: a refused/verdict-less audit is UNAUDITED — surfaced
				// loudly, never a clean bill (Aligned=true) and never a spurious
				// block or drift count (the drift monitor skips "inconclusive").
				onPhase.emit("Align", PhaseWarn, "audit inconclusive (refusal/no verdict): "+v.Concern)
			case blockGate && !v.Aligned && sev == "high":
				// Remove the green light: Accept → Inconclusive with an alignment
				// dissent. The done-gate then refuses to close and the !Accept
				// escalation path below files the mid-run escalation.
				report.Outcome.Verdict = truthalign.Inconclusive
				report.Outcome.Dissenting = append(report.Outcome.Dissenting, "alignment:high:"+v.Concern)
				failureAnalysis = "alignment(high): " + v.Concern
				onPhase.emit("Align", PhaseWarn, "BLOCKED (high, corroborated): "+v.Concern)
			case blockGate && !v.Aligned && sev == "medium":
				// medium (incl. downgraded high): ship (proof is the right-to-ship),
				// but surface to a human. low/none never auto-fails an under-specified
				// spec — it falls through to the advisory default below.
				withLock(stateMu, func() {
					_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
						proj, e := tx.Projects().Active(ctx)
						if e != nil {
							return e
						}
						_, e = tx.Escalations().CreateDetailed(ctx, proj.ID, taskID,
							"alignment review (medium)", v.Concern)
						return e
					})
				})
				onPhase.emit("Align", PhaseWarn, "surfaced (medium): "+v.Concern)
			case v.Aligned:
				onPhase.emit("Align", PhaseDone, "serves the intent")
			default:
				onPhase.emit("Align", PhaseWarn, v.Concern) // advisory (log-only default; low/none)
			}
		} else {
			onPhase.emit("Align", PhaseWarn, "skipped: "+aerr.Error())
		}
	}

	var closed bool
	var gateErr error
	withLock(stateMu, func() { closed, gateErr = New(store).ProveAndCloseReport(ctx, taskID, report) })
	if gateErr != nil {
		return taskResult{}, fmt.Errorf("gate: %w", gateErr)
	}

	// or-v9f.6 (slice A): a task that exhausted refinement files its inbox
	// escalation NOW — on a long backlog run the human learns hours before the
	// epic-level bar, and can act while siblings keep building. Best-effort: an
	// escalation write never fails the build; the bar-time pass dedups via
	// HasOpenForTask.
	if string(report.Outcome.Verdict) != "Accept" {
		var escID string
		withLock(stateMu, func() {
			_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
				proj, e := tx.Projects().Active(ctx)
				if e != nil {
					return e
				}
				// or-gb1.3 PERSIST: the deduped failure-mode row, keyed from the
				// failing obligation + dissenting mode — the CONSULT half reads it
				// at every later task start.
				if _, fe := tx.FailureModes().Record(ctx, proj.ID,
					failureCategory(report), clip(task.ProofObligation, 60), clip(failureSymptom(failureAnalysis), 80)); fe != nil {
					return fe
				}
				id, e := tx.Escalations().CreateDetailed(ctx, proj.ID, taskID,
					fmt.Sprintf("task failed proof after %d attempt(s)", attempts), failureAnalysis)
				if e == nil {
					escID = id
				}
				return e
			})
		})
		// Emit AFTER the tx commits — the teed run-event sink (or-v9f.16)
		// writes to the SAME single-connection store, so an emit inside the
		// transaction deadlocks the build (found by or-tcs.11's e2e).
		if escID != "" {
			onPhase.emit("Escalate", PhaseWarn,
				fmt.Sprintf("escalation %s filed — answer with: orion escalations resolve %s", escID, escID))
		}
		// or-v9f.17: notify AFTER the tx committed (never on rollback) — the
		// mid-run event that lets the human act while siblings keep building.
		if escID != "" {
			_ = notify.Notify(ctx, notify.Event{
				Kind: "escalation.created", Task: taskID, Verdict: string(report.Outcome.Verdict),
				Detail: failureAnalysis, EscalationID: escID,
				NextAction: "orion escalations resolve " + escID,
			})
		}
	}

	// Slice 1 (or-hd3.2): populate memory so a LATER task recalls what was proven,
	// then bound the tier — the context-degradation defense. Best-effort: a memory
	// write miss never fails an otherwise-green build.
	if mem != nil {
		withLock(stateMu, func() {
			memMaintenance(ctx, mem, taskID, buildDir, report, lastNarrative, failureAnalysis, traj)
		})
	}
	// or-7et.5b: on Accept, persist the module's ACTUAL exported surface —
	// extracted from the accepted artifact, keyed by task — for dependent
	// injection and the pre-merge conformance gate. Best-effort.
	if string(report.Outcome.Verdict) == "Accept" {
		var projID string
		withLock(stateMu, func() {
			if proj, _, perr := store.CurrentProjectSpec(ctx); perr == nil {
				projID = proj.ID
			}
		})
		persistModuleSurface(ctx, store, stateMu, projID, taskID, extractModuleSurface(buildDir, task.FileScope))
		// or-tcs.11: the OBSERVED scope (what was actually written) is recorded
		// per task — integration leases prefer it over the declaration.
		if obs := observedWrites(ctx, buildDir); len(obs) > 0 {
			withLock(stateMu, func() {
				_ = store.SaveStringListKind(ctx, projID, contextstore.ObservedScopeKind+taskID, obs)
			})
		}
	}

	return taskResult{
		TaskID: taskID, Report: report, Verdict: string(report.Outcome.Verdict),
		Closed: closed, BuildDir: buildDir, Attempts: attempts,
		FailureAnalysis: failureAnalysis, Alignment: alignment,
	}, nil
}

// epicReprover builds the assembled-tree re-proof closure the integrator runs
// after each merge. A RED verdict is never swallowed into the rollback
// (or-v9f.21): the causal analysis reaches the phase stream (the operator sees
// WHICH obligation went red on the merged tree) and a deduped failure-mode row
// accumulates the pattern across runs. capture receives every report so the
// drift check re-evaluates against the assembled proof.
func epicReprover(store *contextstore.Store, contract testsynth.Contract, model stpa.Model, es spec.ExecutableSpec, requiredIDs []string, projectID string, onPhase PhaseSink, capture func(proof.Report)) func(ctx context.Context, dir string) (bool, error) {
	return func(ctx context.Context, dir string) (bool, error) {
		r, perr := proof.ProveAllWithThreshold(ctx, dir, contract, model, mutationThresholdFor(dir))
		if perr != nil {
			return false, perr
		}
		proof.EnforceObligations(requiredIDs, &r)
		capture(r)
		if string(r.Outcome.Verdict) == string(truthalign.Accept) {
			return true, nil
		}
		analysis := analyzeFailure(r, es.ResponseContract.Cases)
		onPhase.emit("Integrate", PhaseWarn, "assembled-tree re-proof RED — "+analysis)
		_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
			_, e := tx.FailureModes().Record(ctx, projectID, "integration-reproof", "assembled-tree", string(r.Outcome.Verdict))
			return e
		})
		return false, nil
	}
}

// budgetGate refuses new cluster dispatch once the run's budget ceiling is
// reached (or-v9f.18). Wall-clock counts even where a vendor agent's tokens are
// invisible; with no ceiling configured the accountant never halts.
func budgetGate(acct *budget.Accountant) error {
	if acct == nil || !acct.Halted() {
		return nil
	}
	s := acct.Snapshot()
	return fmt.Errorf("budget gate: ceiling reached (tokens=%d dollars=%.2f wall=%s) — no new clusters; raise ORION_BUDGET_* or resolve and re-run", s.Tokens, s.Dollars, s.Wall.Round(time.Second))
}

// recallProofMemo returns a persisted post-enforcement proof Report for an
// (spec anchor, artifact hash) pair (or-v9f.6). Best-effort + nil-safe: a miss,
// a nil store, or an unmarshal error yields ok=false and a fresh proof runs.
// Store access is serialized under stateMu, like every other store touch here.
func recallProofMemo(ctx context.Context, store *contextstore.Store, stateMu *sync.Mutex, specHash, contentHash string) (proof.Report, bool) {
	if store == nil {
		return proof.Report{}, false
	}
	var js string
	var ok bool
	withLock(stateMu, func() {
		var e error
		js, ok, e = store.ProofMemoGet(ctx, specHash, contentHash)
		if e != nil {
			ok = false
		}
	})
	if !ok {
		return proof.Report{}, false
	}
	var r proof.Report
	if err := json.Unmarshal([]byte(js), &r); err != nil {
		return proof.Report{}, false
	}
	return r, true
}

// persistProofMemo records a fresh proof so a later run with identical bytes
// skips it (or-v9f.6). Best-effort: a persist miss never fails the build.
func persistProofMemo(ctx context.Context, store *contextstore.Store, stateMu *sync.Mutex, specHash, contentHash string, r proof.Report) {
	if store == nil {
		return
	}
	js, err := json.Marshal(r)
	if err != nil {
		return
	}
	withLock(stateMu, func() { _ = store.ProofMemoPut(ctx, specHash, contentHash, string(js)) })
}

// mutationThresholdFor classifies the artifact's reliability tier from its own
// source (the same detector fleet the delivery tail uses) and returns the
// mutation-score bar that tier warrants — a Critical artifact is held to 0.9,
// not the hardcoded Standard 0.6 (or-v9f.11).
func mutationThresholdFor(artifactDir string) float64 {
	tier := reliabilitytier.Standard
	if b, err := os.ReadFile(filepath.Join(artifactDir, "main.go")); err == nil {
		findings := reliabilityscan.ScanSource(string(b))
		tier = reliabilitytier.Classify(reliabilityscan.DeriveDimensions(findings))
	}
	return reliabilitytier.MutationThreshold(tier)
}

// memMaintenance records the proven outcome into memory + bounds the tiers (or-hd3.*). Best-effort
// (a memory miss never fails an otherwise-green build); called under stateMu so it is race-free
// when clusters build in parallel.
func memMaintenance(ctx context.Context, mem *memory.Store, taskID, buildDir string, report proof.Report, lastNarrative, failureAnalysis string, traj *buildTrajectory) {
	_ = rememberOutcome(ctx, mem, taskID, report, traj)
	// or-v9f.8: the durable decision log — a proven module's structural choices
	// (exports, routes, module path) persist so dependent modules reuse them.
	_ = rememberDecidedConstraints(ctx, mem, taskID, buildDir, report)
	// or-hd3.5: on a non-Accept verdict, capture WHY it failed so the next attempt avoids it —
	// proof facts trusted, the agent's self-report quarantined. or-7mr: the last attempt's agent
	// narrative (empty for the deterministic fixture) is the untrusted half; rememberFailure tags
	// it generation-tier so it can never reach proof.
	_ = rememberFailure(ctx, mem, taskID, report, lastNarrative, failureAnalysis)
	// or-ykz.8: propose a self-evolution candidate from a passing run (generation-tier,
	// active=false — quarantined AND excluded from recall until the lifecycle activates it).
	_ = proposeCandidate(ctx, mem, taskID, report, traj)
	// or-gb1.4 DISTILL: opt-in LLM distillation of a transferable rule from the
	// trajectory — generation-tier + Candidate, absent unless the flag is set.
	distillRule(ctx, mem, taskID, report, traj)
	// or-hd3.6: promote hot, frequently-recalled MTM patterns to durable LTM FIRST (so eviction
	// can't drop a promotion-eligible item), then bound BOTH tiers — all best-effort.
	_, _, _ = mem.Promote(ctx)
	_ = mem.EvictToCapacity(ctx, memory.MTM, memMTMCapacity)
	_ = mem.EvictToCapacity(ctx, memory.LTM, memLTMCapacity)
	// or-hd3.7: batched (re-)embed of surviving items for semantic recall. No-op unless an
	// embedder is configured (ORION_MEMORY_EMBEDDER); kept off the per-item write path.
	_, _ = mem.Reindex(ctx)
}

// withLock runs f under mu when mu is non-nil (the parallel cluster path); with a nil mu it just
// runs f (the sequential path). Serializes the shared store/memory touchpoints across clusters.
func withLock(mu *sync.Mutex, f func()) {
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}
	f()
}

// maxAgentsFromEnv is the bounded-parallelism cap for independent cluster builds (or-tcs.1.4):
// ORION_MAX_AGENTS when set to a positive int, else 3.
func maxAgentsFromEnv() int {
	if v := strings.TrimSpace(os.Getenv("ORION_MAX_AGENTS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			return n
		}
	}
	return 3
}

// orderByClusters flattens clusters (already in dependency order) into their member
// PlanTasks so coupled tasks are scheduled contiguously. The dependency-correct
// order is still enforced by runDAG's own topological sort; clustering only groups
// the schedule (and, in later slices, assigns each cluster its own worktree).
func orderByClusters(tasks []orchestrator.PlanTask, clusters []decomposer.TaskCluster) []orchestrator.PlanTask {
	byID := make(map[string]orchestrator.PlanTask, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	out := make([]orchestrator.PlanTask, 0, len(tasks))
	seen := make(map[string]bool, len(tasks))
	for _, c := range clusters {
		for _, m := range c.Members {
			if t, ok := byID[m]; ok && !seen[m] {
				out = append(out, t)
				seen[m] = true
			}
		}
	}
	for _, t := range tasks { // any task not placed by clustering keeps its order
		if !seen[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

// provenLoad renders the proven load from the spec's scale dimension.
func provenLoad(es spec.ExecutableSpec) string {
	if th, ok := completeness.ResolveScalePreset(es.Decisions["scale_profile"]); ok {
		return fmt.Sprintf("%d req/%s", th.RequestsPerWindow, th.Window)
	}
	return "unspecified"
}

// faultClasses lists the hazard classes the ratified, controlled UCAs cover.
func faultClasses(m stpa.Model) []string {
	var out []string
	for _, u := range m.UCAs {
		if u.Disposition == stpa.DispositionControlled {
			out = append(out, u.Hazard)
		}
	}
	return out
}

// analyzeFailure renders a CAUSAL analysis of a non-Accept proof Report: which
// declared cases failed (ran but did not satisfy the contract) or never ran (a
// coverage hole), plus the failing modes' raw diagnostic output. It serves two
// readers — the developer (shown in the build report) and the next generation
// attempt (fed back verbatim as fix instructions). The case bodies are rendered
// the same way the generator first saw them, so the model can map an analysis line
// straight back to the behavior it must fix. It never includes proof-corpus source
// (the Report carries only verdicts + the modes' own stderr), so the trust wall
// holds: the generator still cannot read the harness-authored tests.
func analyzeFailure(report proof.Report, cases []spec.BehavioralCase) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Proof verdict: %s.\n", report.Outcome.Verdict)

	var failed, missing []string
	for _, cs := range cases { // stable case order → deterministic analysis
		o, ok := report.ObligationResults[cs.ID]
		switch {
		case !ok || !o.Executed:
			missing = append(missing, strings.TrimSpace(renderCaseForGen(cs)))
		case !o.Passed:
			failed = append(failed, strings.TrimSpace(renderCaseForGen(cs)))
		}
	}
	if len(failed) > 0 {
		b.WriteString("\nFAILING cases (ran, but the response did not satisfy the contract):\n")
		for _, f := range failed {
			b.WriteString("  " + f + "\n")
		}
	}
	if len(missing) > 0 {
		b.WriteString("\nUNEXECUTED cases (the proof could not even run them — likely a crash, wrong route, or missing handler):\n")
		for _, m := range missing {
			b.WriteString("  " + m + "\n")
		}
	}

	// The failing modes' raw output is the most actionable signal — behavioral test
	// failures, the empirical probe's mismatch, the mutation survivors.
	for _, mr := range report.Modes {
		if !mr.Result.Pass && strings.TrimSpace(mr.Result.Output) != "" {
			fmt.Fprintf(&b, "\n%s mode diagnostic:\n%s\n", mr.Result.Mode, indentLines(clip(mr.Result.Output, 1500)))
		}
	}
	if len(report.Outcome.Dissenting) > 0 {
		fmt.Fprintf(&b, "\nDissenting modes: %s\n", strings.Join(report.Outcome.Dissenting, ", "))
	}
	return strings.TrimSpace(b.String())
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "\n… (truncated)"
	}
	return s
}

func indentLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}

// assumptions records the accepted-gap hazards + fallback-preset use so the
// operating envelope states what was NOT proven.
func assumptions(m stpa.Model) []string {
	out := []string{"non-functional dimensions resolved via tier-default fallback presets"}
	for _, u := range m.UCAs {
		if u.Disposition == stpa.DispositionAcceptedGap {
			out = append(out, "accepted gap: "+u.Hazard)
		}
	}
	return out
}
