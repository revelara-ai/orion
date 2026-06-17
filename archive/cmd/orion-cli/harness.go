package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/revelara-ai/orion/internal/architect"
	"github.com/revelara-ai/orion/internal/harness"
)

// harnessCmd implements `orion-cli harness --model=<json> --seed=N`.
// Reads an architectural model from a JSON file (e.g., the output of
// `orion-cli architect --json`), synthesizes a deterministic harness,
// and emits the harness JSON + materialization plan to stdout.
//
// Operator-facing only; the production flow is the orchestrator (E1-8).
type harnessCmd struct {
	stdout io.Writer
	stderr io.Writer
}

func newHarnessCmd(stdout, stderr io.Writer) *harnessCmd {
	return &harnessCmd{stdout: stdout, stderr: stderr}
}

func (c *harnessCmd) Name() string { return "harness" }

func (c *harnessCmd) Synopsis() string {
	return "Synthesize a deterministic harness from an ArchitecturalModel JSON file"
}

func (c *harnessCmd) Run(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("harness", flag.ContinueOnError)
	fs.SetOutput(c.stderr)

	modelFile := fs.String("model", "", "path to architect.ArchitecturalModel JSON (required)")
	runID := fs.String("run", "", "run ID (required; namespace = orion-run-<short>)")
	seed := fs.Int64("seed", 1, "deterministic seed for synthesis")
	thoroughness := fs.String("thoroughness", "standard", "fast | standard | thorough")

	fs.Usage = func() {
		_, _ = fmt.Fprintf(c.stderr, "Usage: %s harness --model=<file> --run=<id> [--seed=N] [--thoroughness=tier]\n\n", progName)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *modelFile == "" || *runID == "" {
		_, _ = fmt.Fprintln(c.stderr, "error: --model and --run are required")
		fs.Usage()
		return 2
	}

	model, err := loadModel(*modelFile)
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "error: %v\n", err)
		return 2
	}
	h, err := harness.Synthesize(harness.SynthesizeOptions{
		RunID:        *runID,
		Model:        model,
		Seed:         *seed,
		Thoroughness: harness.Thoroughness(*thoroughness),
	})
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "error: synthesize: %v\n", err)
		if errors.Is(err, harness.ErrInvalidInputs) {
			return 2
		}
		return 1
	}
	plan, err := harness.Materialize(h)
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "error: materialize: %v\n", err)
		return 1
	}
	out := struct {
		Harness             *harness.Harness `json:"harness"`
		MaterializationYAML string           `json:"materialization_yaml"`
		TeardownCommand     string           `json:"teardown_command"`
	}{
		Harness:             h,
		MaterializationYAML: plan.ManifestYAML,
	}
	if td, terr := harness.Teardown(h); terr == nil {
		out.TeardownCommand = td.DeleteCommand
	}
	enc := json.NewEncoder(c.stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		_, _ = fmt.Fprintf(c.stderr, "error: encode: %v\n", err)
		return 1
	}
	return 0
}

func loadModel(path string) (*architect.ArchitecturalModel, error) {
	body, err := os.ReadFile(path) //#nosec G304 -- path is operator-supplied
	if err != nil {
		return nil, fmt.Errorf("read model: %w", err)
	}
	var m architect.ArchitecturalModel
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("decode model: %w", err)
	}
	return &m, nil
}
