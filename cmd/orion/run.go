package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/revelara-ai/orion/internal/agentruntime"
	"github.com/revelara-ai/orion/internal/conductor"
	"github.com/revelara-ai/orion/internal/contextstore"
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

	// One-shot build→prove→deliver, shared with the native Orion agent's
	// build_service tool. generateService injects the (opt-in) vendor-agent
	// generator; nil would use the fixture.
	res, err := conductor.BuildAndProve(ctx, store, generateService, func(s string) { fmt.Println("run:", s) })
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion run:", err)
		return 1
	}
	fmt.Printf("run: task %s verdict=%s closed=%v tier=%s delivery=%s\n", res.TaskID, res.Verdict, res.Closed, res.Tier, res.Delivery)
	return 0
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
