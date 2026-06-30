package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/revelara-ai/orion/internal/actuation"
	"github.com/revelara-ai/orion/internal/contextengine"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/delivery"
	"github.com/revelara-ai/orion/internal/lspcheck"
	"github.com/revelara-ai/orion/internal/memory"
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
	Tier            string
	Delivery        string // "deliver" | "escalate"
	Reason          string // escalation reason, if any
	BuildDir        string
	OutputDir       string          // where the proven code was written in the dev's repo (Accept only)
	Git             GitDelivery     // git commit/branch of the proven code (when ORION_GIT_DELIVERY)
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
	return BuildDAG(ctx, store, gen, aligner, onPhase, outRoot)
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
func BuildDAG(ctx context.Context, store *contextstore.Store, gen Generator, aligner Aligner, onPhase PhaseSink, outRoot string) (BuildResult, error) {
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
	requiredIDs := es.ResponseContract.RequiredCaseIDs()

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
	clusterWT, cleanupWT, werr := clusterWorktreeSet(ctx, wtMgr, clusters, managed.Base)
	if werr != nil {
		return BuildResult{}, fmt.Errorf("worktree isolation: %w", werr)
	}
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
			eng = contextengine.New(store, mem)
			defer func() { _ = mem.Close() }()
		}
	}

	// or-tcs.1.4: independent clusters build CONCURRENTLY (bounded by ORION_MAX_AGENTS, default 3),
	// each in its own worktree; the shared store/memory writes serialize on stateMu and the phase
	// sink is made concurrency-safe, so the only parallelism is the expensive generate+prove work.
	maxConc := maxAgentsFromEnv()
	safeSink := syncSink(onPhase)
	var stateMu sync.Mutex
	results, err := runClusterDAG(clusters, scheduleTasks, maxConc, func(task orchestrator.PlanTask, cache map[string]proof.Report) (taskResult, error) {
		buildDir := clusterWT[clusterByTask[task.ID]]
		if buildDir == "" {
			return taskResult{}, fmt.Errorf("task %s has no cluster worktree", task.ID)
		}
		return buildOneTask(ctx, store, gen, aligner, safeSink, es, model, gs, contract, requiredIDs, buildDir, cache, eng, mem, &stateMu, task)
	})
	if err != nil {
		return BuildResult{}, err
	}

	// or-tcs.1.6: assemble the proven clusters. Each accepted cluster integrates ONE AT A TIME
	// onto a fresh epic head, re-proving the merged tree (proof.ProveAll), so the DELIVERED
	// artifact is the integrated whole — not one cluster's tree. A merge conflict or a red
	// post-merge re-proof fails the epic. The Epic is Accept only if every task Accepted AND the
	// assembly held.
	reprove := func(ctx context.Context, dir string) (bool, error) {
		r, perr := proof.ProveAll(ctx, dir, contract, model)
		if perr != nil {
			return false, perr
		}
		proof.EnforceObligations(requiredIDs, &r)
		return string(r.Outcome.Verdict) == string(truthalign.Accept), nil
	}
	intDir, integrated, ierr := integrateEpic(ctx, wtMgr, clusters, clusterWT, results, managed.Base, reprove, onPhase)
	if ierr != nil {
		return BuildResult{}, fmt.Errorf("epic integration: %w", ierr)
	}
	aggregateVerdict := truthalign.Accept
	for _, r := range results {
		if r.Verdict != "Accept" {
			aggregateVerdict = truthalign.Reject
			break
		}
	}
	if !integrated {
		aggregateVerdict = truthalign.Reject
	}
	// or-tcs.3: system-assembly proof — the per-task proofs do not imply the ASSEMBLED system is
	// wired. On the integrated tree, every package must be reachable from a main; an orphan (built
	// but unwired — the "6 orphan packages" lesson) REJECTS the epic even if every task Accepted.
	if integrated {
		if wired, orphans := systemWireupGate(intDir); !wired {
			aggregateVerdict = truthalign.Reject
			onPhase.emit("SystemValidate", PhaseWarn, fmt.Sprintf("orphan package(s) not wired into the system: %v", orphans))
		} else {
			onPhase.emit("SystemValidate", PhaseDone, "assembled wireup clean (no orphan packages)")
		}
	}
	buildDir := intDir // the INTEGRATED tree is the single delivery artifact
	// Representative task result for the epic-level report fields (id, attempts, alignment).
	rep := results[0]
	for _, r := range results {
		if r.Verdict == "Accept" {
			rep = r
			break
		}
	}
	taskID := rep.TaskID

	// Reliability scan → tier → deployment bar → deliver or escalate (Epic-level, once).
	findings, _ := reliabilityscan.ScanAndRecord(ctx, store, proj.ID, buildDir)
	tier := reliabilitytier.Classify(reliabilityscan.DeriveDimensions(findings))
	env := delivery.OperatingEnvelope{
		ProvenLoad:             provenLoad(es),
		FaultClassesControlled: faultClasses(model),
		Assumptions:            assumptions(model),
	}
	securityOK := proof.SecurityClean(buildDir)
	res := delivery.EvaluateBar(aggregateVerdict, []string{"behavioral", "empirical", "hazard"}, reliabilitytier.PolicyFor(tier), env, securityOK)
	// Red Button (or-utm): while engaged, autonomy is revoked — never auto-deliver.
	if rb := (actuation.RedButton{Path: filepath.Join(store.Dir(), "red_button")}); res.Decision == delivery.Deliver && rb.AutonomyRevoked() {
		res = delivery.Result{Decision: delivery.Escalate, Reason: "red button engaged: autonomy revoked, human delivery required"}
	}
	if res.Decision == delivery.Deliver {
		envJSON, _ := json.Marshal(res.Envelope)
		runbook := delivery.GenerateRunbook(es, model, env)
		rbJSON, _ := json.Marshal(runbook)
		_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
			epic, e := tx.Epics().LatestForProject(ctx, proj.ID)
			if e != nil {
				return e
			}
			_, e = tx.Deliveries().Create(ctx, epic.ID, string(envJSON), string(rbJSON))
			return e
		})
	} else {
		_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
			_, e := tx.Escalations().Create(ctx, proj.ID, taskID, res.Reason)
			return e
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
	if aggregateVerdict == truthalign.Accept && outRoot != "" {
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
				}
			} else {
				onPhase.emit("Deliver", PhaseWarn, "git delivery requested but the working directory is not a git repo")
			}
		}
	}

	return BuildResult{
		TaskID: taskID, Verdict: string(aggregateVerdict), Closed: rep.Closed,
		OutputDir: outputDir,
		Tier:      string(tier), Delivery: string(res.Decision), Reason: res.Reason, BuildDir: buildDir,
		Git:       gitDelivery,
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
			report = proof.Report{}
			report.Outcome.Verdict = truthalign.Inconclusive
			if attempt < maxBuildAttempts {
				continue
			}
			break
		}

		// Proof is memoized by artifact content hash: an identical artifact (e.g. a
		// sibling cluster building the same code) is proven once — the verdict is
		// deterministic — so the DAG never re-proves the same bytes N times.
		var rep proof.Report
		if cached, ok := proofCache[art.ContentHash]; ok {
			rep = cached
			onPhase.emit("Prove", PhaseDone, "reused (identical artifact already proven)")
		} else {
			onPhase.emit("Prove", PhaseRunning, "behavioral + empirical + hazard")
			r, rerr := proof.ProveAll(ctx, buildDir, contract, model)
			if rerr != nil {
				onPhase.emit("Prove", PhaseFailed, rerr.Error())
				return taskResult{}, fmt.Errorf("proof: %w", rerr)
			}
			// Coverage gate: every requirement the spec declares must have an executed,
			// passing obligation — else downgrade the verdict (the or-y9d kill).
			proof.EnforceObligations(requiredIDs, &r)
			proofCache[art.ContentHash] = r
			rep = r
		}
		report = rep

		if string(report.Outcome.Verdict) == "Accept" {
			d := "Accept"
			if attempt > 1 {
				d = fmt.Sprintf("Accept (attempt %d)", attempt)
			}
			onPhase.emit("Prove", PhaseDone, d)
			failureAnalysis = ""
			break
		}

		// Reject/Inconclusive → causal analysis becomes the next attempt's feedback.
		failureAnalysis = analyzeFailure(report, es.ResponseContract.Cases)
		feedback = failureAnalysis
		if attempt < maxBuildAttempts {
			onPhase.emit("Prove", PhaseWarn, fmt.Sprintf("%s (attempt %d/%d) — analyzing failure, refining", report.Outcome.Verdict, attempt, maxBuildAttempts))
		} else {
			onPhase.emit("Prove", PhaseWarn, fmt.Sprintf("%s after %d attempts — escalating", report.Outcome.Verdict, maxBuildAttempts))
		}
	}

	// AlignmentGate (V3 Step 1, LOG-ONLY): an advisory audit of whether the built
	// code serves the INTENT, not just the cases. It records + surfaces a concern but
	// does NOT change the verdict or block delivery yet (proof.Accept stays the sole
	// right-to-ship).
	var alignment AlignmentRecord
	if aligner != nil {
		onPhase.emit("Align", PhaseRunning, "")
		if v, aerr := aligner(ctx, es.Intent, buildDir, es.ResponseContract.Cases); aerr == nil {
			alignment = AlignmentRecord{Ran: true, Aligned: v.Aligned, Severity: v.Severity, Concern: v.Concern}
			if v.Aligned {
				onPhase.emit("Align", PhaseDone, "serves the intent")
			} else {
				onPhase.emit("Align", PhaseWarn, v.Concern)
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

	// Slice 1 (or-hd3.2): populate memory so a LATER task recalls what was proven,
	// then bound the tier — the context-degradation defense. Best-effort: a memory
	// write miss never fails an otherwise-green build.
	if mem != nil {
		withLock(stateMu, func() {
			memMaintenance(ctx, mem, taskID, report, lastNarrative)
		})
	}

	return taskResult{
		TaskID: taskID, Report: report, Verdict: string(report.Outcome.Verdict),
		Closed: closed, BuildDir: buildDir, Attempts: attempts,
		FailureAnalysis: failureAnalysis, Alignment: alignment,
	}, nil
}

// memMaintenance records the proven outcome into memory + bounds the tiers (or-hd3.*). Best-effort
// (a memory miss never fails an otherwise-green build); called under stateMu so it is race-free
// when clusters build in parallel.
func memMaintenance(ctx context.Context, mem *memory.Store, taskID string, report proof.Report, lastNarrative string) {
	_ = rememberOutcome(ctx, mem, taskID, report)
	// or-hd3.5: on a non-Accept verdict, capture WHY it failed so the next attempt avoids it —
	// proof facts trusted, the agent's self-report quarantined. or-7mr: the last attempt's agent
	// narrative (empty for the deterministic fixture) is the untrusted half; rememberFailure tags
	// it generation-tier so it can never reach proof.
	_ = rememberFailure(ctx, mem, taskID, report, lastNarrative)
	// or-ykz.8: propose a self-evolution candidate from a passing run (generation-tier,
	// active=false — quarantined AND excluded from recall until the lifecycle activates it).
	_ = proposeCandidate(ctx, mem, taskID, report)
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
