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
	TaskID   string
	Verdict  string // converged proof verdict (Accept/Reject)
	Closed   bool   // task closed (verification-gated done)
	Tier     string
	Delivery string // "deliver" | "escalate"
	Reason   string // escalation reason, if any
	BuildDir string
}

// BuildAndProve builds the current accepted spec's lead task and proves it: it
// decomposes on demand, generates the service, runs multi-modal proof
// (behavioral + empirical + hazard), records verdicts, gates task closure, then
// runs the reliability scan → tier → deployment bar → deliver/escalate. This is
// the one-shot "build to the spec" pipeline shared by `orion run` and the native
// Orion agent's build_service tool. gen==nil uses the deterministic fixture;
// onStep (may be nil) streams progress lines to the conversation.
func BuildAndProve(ctx context.Context, store *contextstore.Store, gen Generator, onStep func(string)) (BuildResult, error) {
	step := func(s string) {
		if onStep != nil {
			onStep(s)
		}
	}
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
	pv, err := c.PlanView(ctx) // decomposes + persists on demand
	if err != nil || len(pv.Tasks) == 0 {
		return BuildResult{}, fmt.Errorf("no plan: %w", err)
	}
	taskID := pv.Tasks[0].ID

	gs := sandbox.GenSpec{
		Route:    es.ResponseContract.Route,
		Port:     es.ResponseContract.Port,
		Format:   es.ResponseContract.Format(), // anchored contract is the source of truth
		TimeZone: es.ResponseContract.TimeZone,
	}
	buildDir := filepath.Join(store.Dir(), "build", taskID)
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return BuildResult{}, fmt.Errorf("build dir: %w", err)
	}

	step("Generating the service…")
	art, err := gen(ctx, gs, buildDir)
	if err != nil {
		return BuildResult{}, fmt.Errorf("generate: %w", err)
	}
	if _, err := sandbox.PersistArtifact(ctx, store, taskID, art); err != nil {
		return BuildResult{}, fmt.Errorf("persist artifact: %w", err)
	}

	proj, _, _ := store.CurrentProjectSpec(ctx)
	model, ok, _ := stpa.Load(ctx, store, proj.ID)
	if !ok {
		model = stpa.RatifiedTimeServiceModel()
		_ = stpa.Save(ctx, store, proj.ID, model)
	}

	step("Proving (behavioral + empirical + hazard)…")
	report, err := proof.ProveAll(ctx, buildDir, testsynth.Contract{Route: gs.Route, Format: gs.Format, TimeZone: gs.TimeZone, Cases: es.ResponseContract.Cases}, model)
	if err != nil {
		return BuildResult{}, fmt.Errorf("proof: %w", err)
	}
	// Coverage gate: every requirement the spec declares must have an executed,
	// passing obligation — else downgrade the verdict (the or-y9d kill).
	proof.EnforceObligations(es.ResponseContract.RequiredCaseIDs(), &report)
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

	return BuildResult{
		TaskID: taskID, Verdict: string(report.Outcome.Verdict), Closed: closed,
		Tier: string(tier), Delivery: string(res.Decision), Reason: res.Reason, BuildDir: buildDir,
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
