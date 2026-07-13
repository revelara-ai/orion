package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/revelara-ai/orion/internal/agentruntime"
	"github.com/revelara-ai/orion/internal/conductor"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/llmsetup"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// cmdRun executes the V2.0 loop for the current accepted spec's lead task:
// generate the service into a build dir, run multi-modal proof
// (behavioral + empirical), record the verdicts, and close the task only if the
// converged verdict is Accept (verification-gated done).
func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	mode := fs.String("mode", "text", "output mode: text | json (typed JSONL event stream)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
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
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	// One-shot build→prove→deliver, shared with the native Orion agent's
	// build_service tool. generateService injects the (opt-in) vendor-agent
	// generator; nil would use the fixture.
	// or-kzf.1: the alignment judge was NIL on this path — the single
	// semantic-drift detector never ran for `orion run`. Wire it from the
	// session brain (independent model honored via ORION_ALIGN_MODEL);
	// offline stays nil — the deterministic fixture path needs no judge.
	var aligner conductor.Aligner
	if brain := llmsetup.Select(); brain.Provider != nil {
		aligner = conductor.NativeAligner(conductor.AlignJudgeProvider(brain.Provider))
	}
	sink := func(e conductor.PhaseEvent) { fmt.Printf("run: %s %s %s\n", e.Status, e.Phase, e.Detail) }
	if *mode == "json" {
		sink = jsonSink(os.Stdout)
	}
	res, err := conductor.BuildAndProve(ctx, store, generateService, aligner, sink, conductor.OutputRoot())
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion run:", err)
		return 1
	}
	if *mode == "json" {
		emitRunResult(res)
	} else {
		fmt.Printf("run: task %s verdict=%s closed=%v tier=%s delivery=%s\n", res.TaskID, res.Verdict, res.Closed, res.Tier, res.Delivery)
	}
	// or-v9f.17: the end-of-run notification now fires inside BuildDAG itself, so
	// every entry point (this CLI, the TUI's build_service, headless) inherits it.
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
	defer func() { _ = store.Close() }()

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
func generateService(ctx context.Context, gs sandbox.GenSpec, buildDir, feedback string) (sandbox.GeneratedArtifact, error) {
	presets, ok := configuredAgentChain()
	if !ok {
		return sandbox.GenerateTimeServiceFixture(buildDir, gs)
	}
	// or-ykz.13: an ordered FAILOVER CHAIN — each entry gets one deadline-
	// bounded turn; rate-limit/overload/quota/hang/refusal advances to the
	// next vendor with a visible notice. One preset degenerates to today.
	chain := make([]agentruntime.NamedGenerator, 0, len(presets))
	for _, p := range presets {
		chain = append(chain, agentruntime.NamedGenerator{
			Name: p.Name,
			Gen:  agentruntime.AgentGenerator{Driver: agentruntime.SpawnDriver(p, generationRole(gs), nil)},
		})
	}
	gen := agentruntime.FailoverGenerator{Chain: chain, OnFailover: func(from, to, reason string) {
		fmt.Printf("run: agent %s failed over to %s (%s)\n", from, to, reason)
		slog.Warn("vendor-agent failover", "from", from, "to", to, "reason", reason)
	}}
	desc := "implement the ratified service"
	if feedback != "" { // refinement attempt — give the agent the proof's causal analysis
		desc = "fix the prior implementation; the independent proof rejected it.\n\n" + feedback
	}
	req := agentruntime.GenRequest{
		Description: desc,
		Module:      "orion-generated/svc",
		Route:       gs.Route, Port: gs.Port, Format: gs.Format, TimeZone: gs.TimeZone,
	}
	agentArt, err := gen.Generate(ctx, req, buildDir)
	if err != nil {
		return sandbox.GeneratedArtifact{}, fmt.Errorf("agent generation: %w", err)
	}
	art, err := sandbox.ArtifactFromDir(buildDir)
	if err != nil {
		return art, err
	}
	art.Narrative = agentArt.Narrative // carry the agent's self-report (or-7mr)
	return art, nil
}

// configuredAgentChain returns the opt-in vendor-agent FAILOVER CHAIN
// (ORION_AGENT="claude,gemini,codex" — or a single name, exactly as before).
// Unknown/off-PATH entries are skipped with a warning; an empty resolved
// chain falls back to the fixture. Opt-in so `orion run` never silently
// spawns an agent or uses quota.
func configuredAgentChain() ([]agentruntime.Preset, bool) {
	return resolveAgentChain(os.Getenv("ORION_AGENT"), exec.LookPath)
}

// resolveAgentChain is configuredAgentChain with an injectable PATH lookup.
func resolveAgentChain(names string, lookPath func(string) (string, error)) ([]agentruntime.Preset, bool) {
	var out []agentruntime.Preset
	for _, name := range strings.Split(names, ",") {
		if p, ok := resolveAgent(name, lookPath); ok {
			out = append(out, p)
		} else if n := strings.TrimSpace(name); n != "" && !strings.EqualFold(n, "none") && !strings.EqualFold(n, "fixture") {
			fmt.Fprintf(os.Stderr, "orion run: agent %q unknown or not on PATH — skipped in the failover chain\n", n)
		}
	}
	return out, len(out) > 0
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

// generationRole primes a spawned vendor agent with the SAME general, case-driven,
// reliability-focused prompt as the native path (one shared builder) — not a
// time-service-specific prompt. The only difference is the file-write instruction
// (this path writes via the ACP fs/write_text_file tool). (or-3ba.7)
func generationRole(gs sandbox.GenSpec) string {
	return conductor.GenerationPrompt(gs, "Write go.mod and main.go into the working directory via fs/write_text_file, then end the turn.")
}
