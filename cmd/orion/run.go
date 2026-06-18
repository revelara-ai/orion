package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/revelara-ai/orion/internal/conductor"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
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
		Route:    es.ResponseContract.Route,
		Port:     es.ResponseContract.Port,
		Format:   es.Decisions["response_format"],
		TimeZone: es.ResponseContract.TimeZone,
	}
	buildDir := filepath.Join(store.Dir(), "build", taskID)
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "orion run: build dir:", err)
		return 1
	}
	art, err := sandbox.GenerateGoTimeService(buildDir, gs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion run: generate:", err)
		return 1
	}
	if _, err := sandbox.PersistArtifact(ctx, store, taskID, art); err != nil {
		fmt.Fprintln(os.Stderr, "orion run: persist artifact:", err)
		return 1
	}

	report, err := proof.Prove(ctx, buildDir, testsynth.Contract{Route: gs.Route, Format: gs.Format, TimeZone: gs.TimeZone})
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion run: proof:", err)
		return 1
	}
	closed, err := conductor.New(store).ProveAndCloseReport(ctx, taskID, report)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion run: gate:", err)
		return 1
	}
	fmt.Printf("run: task %s verdict=%s closed=%v\n", taskID, report.Outcome.Verdict, closed)
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
