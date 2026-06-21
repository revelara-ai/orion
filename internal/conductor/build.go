package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/revelara-ai/orion/internal/actuation"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/delivery"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/reliabilityscan"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// Generator produces the service for a spec into buildDir. The default is the
// deterministic fixture; cmd/orion injects a vendor-agent generator when opted in.
type Generator func(ctx context.Context, gs sandbox.GenSpec, buildDir string) (sandbox.GeneratedArtifact, error)

// BuildResult is the outcome of building + proving a ratified spec.
type BuildResult struct {
	TaskID    string
	Verdict   string // converged proof verdict (Accept/Reject)
	Closed    bool   // task closed (verification-gated done)
	Tier      string
	Delivery  string // "deliver" | "escalate"
	Reason    string // escalation reason, if any
	BuildDir  string
	Alignment AlignmentRecord // advisory intent-alignment audit (log-only in V3 Step 1)
}

// BuildAndProve builds the current accepted spec's lead task and proves it: it
// decomposes on demand, generates the service, runs multi-modal proof
// (behavioral + empirical + hazard), records verdicts, gates task closure, then
// runs the reliability scan → tier → deployment bar → deliver/escalate. This is
// the one-shot "build to the spec" pipeline shared by `orion run` and the native
// Orion agent's build_service tool. gen==nil uses the deterministic fixture;
// onStep (may be nil) streams progress lines to the conversation.
func BuildAndProve(ctx context.Context, store *contextstore.Store, gen Generator, aligner Aligner, onPhase PhaseSink) (BuildResult, error) {
	if gen == nil {
		gen = func(_ context.Context, gs sandbox.GenSpec, dir string) (sandbox.GeneratedArtifact, error) {
			return sandbox.GenerateFixtureService(dir, gs)
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
	taskID := pv.Tasks[0].ID
	onPhase.emit("Decompose", PhaseDone, fmt.Sprintf("%d task(s)", len(pv.Tasks)))

	gs := sandbox.GenSpec{
		Route:    es.ResponseContract.Route,
		Port:     es.ResponseContract.Port,
		Format:   es.ResponseContract.Format(), // anchored contract is the source of truth
		TimeZone: es.ResponseContract.TimeZone,
		Cases:    es.ResponseContract.Cases, // the behavioral contract the generator builds to
	}
	buildDir := filepath.Join(store.Dir(), "build", taskID)
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return BuildResult{}, fmt.Errorf("build dir: %w", err)
	}

	onPhase.emit("Generate", PhaseRunning, "")
	art, err := gen(ctx, gs, buildDir)
	if err != nil {
		onPhase.emit("Generate", PhaseFailed, err.Error())
		return BuildResult{}, fmt.Errorf("generate: %w", err)
	}
	if _, err := sandbox.PersistArtifact(ctx, store, taskID, art); err != nil {
		return BuildResult{}, fmt.Errorf("persist artifact: %w", err)
	}
	onPhase.emit("Generate", PhaseDone, "")

	proj, _, _ := store.CurrentProjectSpec(ctx)
	model, ok, _ := stpa.Load(ctx, store, proj.ID)
	if !ok {
		model = stpa.RatifiedTimeServiceModel()
		_ = stpa.Save(ctx, store, proj.ID, model)
	}

	onPhase.emit("Prove", PhaseRunning, "behavioral + empirical + hazard")
	report, err := proof.ProveAll(ctx, buildDir, testsynth.Contract{Route: gs.Route, Format: gs.Format, TimeZone: gs.TimeZone, Cases: es.ResponseContract.Cases}, model)
	if err != nil {
		onPhase.emit("Prove", PhaseFailed, err.Error())
		return BuildResult{}, fmt.Errorf("proof: %w", err)
	}
	// Coverage gate: every requirement the spec declares must have an executed,
	// passing obligation — else downgrade the verdict (the or-y9d kill).
	proof.EnforceObligations(es.ResponseContract.RequiredCaseIDs(), &report)
	proveStatus := PhaseDone
	if string(report.Outcome.Verdict) != "Accept" {
		proveStatus = PhaseWarn
	}
	onPhase.emit("Prove", proveStatus, string(report.Outcome.Verdict))

	// AlignmentGate (V3 Step 1, LOG-ONLY): an advisory audit of whether the built
	// code serves the INTENT, not just the cases. It records + surfaces a concern
	// but does NOT change the verdict or block delivery yet — this step validates
	// the judge before it is allowed to gate (proof.Accept stays the sole
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

	closed, err := New(store).ProveAndCloseReport(ctx, taskID, report)
	if err != nil {
		return BuildResult{}, fmt.Errorf("gate: %w", err)
	}

	// Reliability scan → tier → deployment bar → deliver or escalate.
	findings, _ := reliabilityscan.ScanAndRecord(ctx, store, proj.ID, buildDir)
	tier := reliabilitytier.Classify(reliabilityscan.DeriveDimensions(findings))
	env := delivery.OperatingEnvelope{
		ProvenLoad:             provenLoad(es),
		FaultClassesControlled: faultClasses(model),
		Assumptions:            assumptions(model),
	}
	securityOK := proof.SecurityClean(buildDir)
	res := delivery.EvaluateBar(report.Outcome.Verdict, []string{"behavioral", "empirical", "hazard"}, reliabilitytier.PolicyFor(tier), env, securityOK)
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

	return BuildResult{
		TaskID: taskID, Verdict: string(report.Outcome.Verdict), Closed: closed,
		Tier: string(tier), Delivery: string(res.Decision), Reason: res.Reason, BuildDir: buildDir,
		Alignment: alignment,
	}, nil
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
