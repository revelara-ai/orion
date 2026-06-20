package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/actuation"
	"github.com/revelara-ai/orion/internal/agentruntime"
	"github.com/revelara-ai/orion/internal/conductor"
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

// cmdRun executes the V2.0 loop for the current accepted spec's lead task:
// generate the service into a build dir, run multi-modal proof
// (behavioral + empirical), record the verdicts, and close the task only if the
// converged verdict is Accept (verification-gated done).
func cmdRun(_ []string) int {
	dir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion run:", err)
		return 1
	}
	store, err := contextstore.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion run:", err)
		return 1
	}
	defer store.Close()
	ctx := context.Background()

	c := orchestrator.NewWithStore(store)
	es, err := c.RecallSpec(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion run: no accepted spec (submit → answer → spec approve first):", err)
		return 1
	}
	pv, err := c.PlanView(ctx)
	if err != nil || len(pv.Tasks) == 0 {
		fmt.Fprintln(os.Stderr, "orion run: no plan:", err)
		return 1
	}
	taskID := pv.Tasks[0].ID

	gs := sandbox.GenSpec{
		Route: es.ResponseContract.Route,
		Port:  es.ResponseContract.Port,
		// Format from the ANCHORED contract, not the raw decision string — codegen
		// + proof must build/prove the exact format the ratified contract promises.
		Format:   es.ResponseContract.Format(),
		TimeZone: es.ResponseContract.TimeZone,
	}
	buildDir := filepath.Join(store.Dir(), "build", taskID)
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "orion run: build dir:", err)
		return 1
	}
	art, err := generateService(ctx, gs, buildDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion run: generate:", err)
		return 1
	}
	if _, err := sandbox.PersistArtifact(ctx, store, taskID, art); err != nil {
		fmt.Fprintln(os.Stderr, "orion run: persist artifact:", err)
		return 1
	}

	// Load the developer-ratified STPA model for hazard proof; for the canonical
	// time-service path, fall back to the golden ratified model and persist it.
	proj, _, _ := store.CurrentProjectSpec(ctx)
	model, ok, _ := stpa.Load(ctx, store, proj.ID)
	if !ok {
		model = stpa.RatifiedTimeServiceModel()
		_ = stpa.Save(ctx, store, proj.ID, model)
	}
	report, err := proof.ProveAll(ctx, buildDir, testsynth.Contract{Route: gs.Route, Format: gs.Format, TimeZone: gs.TimeZone}, model)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion run: proof:", err)
		return 1
	}
	closed, err := conductor.New(store).ProveAndCloseReport(ctx, taskID, report)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion run: gate:", err)
		return 1
	}

	// Reliability scan → tier → deployment bar → deliver (human-mergeable) or escalate.
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
	fmt.Printf("run: task %s verdict=%s closed=%v tier=%s delivery=%s\n", taskID, report.Outcome.Verdict, closed, tier, res.Decision)
	return 0
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

// cmdProof implements `orion proof show --task <id> --mode <mode> [--json]`.
func cmdProof(args []string) int {
	if len(args) == 0 || args[0] != "show" {
		fmt.Fprintln(os.Stderr, "orion proof: expected 'show'")
		return 2
	}
	fs := flag.NewFlagSet("proof show", flag.ContinueOnError)
	task := fs.String("task", "", "task id")
	mode := fs.String("mode", "converged", "proof mode (behavioral|empirical|hazard|converged)")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *task == "" {
		fmt.Fprintln(os.Stderr, "orion proof show: --task is required")
		return 2
	}
	dir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion proof show:", err)
		return 1
	}
	store, err := contextstore.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion proof show:", err)
		return 1
	}
	defer store.Close()

	p, err := store.ProofByTaskMode(context.Background(), *task, *mode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "orion proof show: no %s proof for task %s\n", *mode, *task)
		return 1
	}
	// Merge mode-specific detail with the verdict + metrics for a flat view.
	out := map[string]any{}
	_ = json.Unmarshal([]byte(p.Detail), &out)
	out["mode"] = p.Mode
	out["verdict"] = p.Verdict
	out["mutation_score"] = p.MutationScore
	out["empirical_pass_rate"] = p.EmpiricalPassRate
	if *asJSON {
		return emitJSON(out)
	}
	fmt.Printf("proof %s for %s: verdict=%s detail=%s\n", p.Mode, *task, p.Verdict, p.Detail)
	return 0
}

// generateService produces the service for the spec into buildDir. By default it
// uses the deterministic fixture; with ORION_AGENT=<preset> set (and the agent on
// PATH) it spawns the developer's own vendor coding-agent to WRITE the code over
// ACP — the real "Orion writes new code" dogfood path (or-s10).
func generateService(ctx context.Context, gs sandbox.GenSpec, buildDir string) (sandbox.GeneratedArtifact, error) {
	preset, ok := configuredAgent()
	if !ok {
		return sandbox.GenerateFixtureService(buildDir, gs)
	}
	gen := agentruntime.AgentGenerator{Driver: agentruntime.SpawnDriver(preset, generationRole(gs), nil)}
	req := agentruntime.GenRequest{
		Description: "implement the ratified service",
		Module:      "orion-generated/svc",
		Route:       gs.Route, Port: gs.Port, Format: gs.Format, TimeZone: gs.TimeZone,
	}
	if _, err := gen.Generate(ctx, req, buildDir); err != nil {
		return sandbox.GeneratedArtifact{}, fmt.Errorf("agent generation: %w", err)
	}
	return sandbox.ArtifactFromDir(buildDir)
}

// configuredAgent returns the opt-in vendor agent preset (ORION_AGENT=<name>) when
// it is set, known, and on PATH; otherwise generation uses the deterministic
// fixture. Opt-in so `orion run` never silently spawns an agent or uses quota.
func configuredAgent() (agentruntime.Preset, bool) {
	return resolveAgent(os.Getenv("ORION_AGENT"), exec.LookPath)
}

// resolveAgent is configuredAgent with an injectable PATH lookup (for tests).
func resolveAgent(name string, lookPath func(string) (string, error)) (agentruntime.Preset, bool) {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "none") || strings.EqualFold(name, "fixture") {
		return agentruntime.Preset{}, false
	}
	p, ok := agentruntime.DefaultPresetRegistry().Get(name)
	if !ok {
		return p, false
	}
	if _, err := lookPath(p.Command); err != nil {
		return p, false
	}
	return p, true
}

// generationRole primes a spawned vendor agent to write the contract-conformant
// service (exposing handleTime, the proof harness's stable contract symbol).
func generationRole(gs sandbox.GenSpec) string {
	return fmt.Sprintf("You are Orion's code generator. Write a complete, compilable Go HTTP service that serves route %s returning %s, timezone %s, honoring a PORT env override. Expose the request handler as a top-level func handleTime(w http.ResponseWriter, r *http.Request). Write go.mod and main.go into the working directory via fs/write_text_file, then end the turn.", gs.Route, gs.Format, gs.TimeZone)
}
