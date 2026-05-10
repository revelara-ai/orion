package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/revelara-ai/orion/internal/enrichment"
	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/patches"
	"github.com/revelara-ai/orion/internal/polaris"
)

// synthCmd implements `orion-cli synth --gaps=<file>`. The gaps file is
// a JSON array of patches.Gap; the command builds a per-gap context
// block from Polaris (snapshotted), prompts the LLM once per gap, and
// emits the resulting CandidatePatches as JSON to stdout.
//
// Operator-facing only; the production flow is the orchestrator (E1-8).
type synthCmd struct {
	stdout io.Writer
	stderr io.Writer
}

func newSynthCmd(stdout, stderr io.Writer) *synthCmd {
	return &synthCmd{stdout: stdout, stderr: stderr}
}

func (c *synthCmd) Name() string { return "synth" }

func (c *synthCmd) Synopsis() string {
	return "Generate LLM-synthesized patches for gaps in a JSON file"
}

func (c *synthCmd) Run(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("synth", flag.ContinueOnError)
	fs.SetOutput(c.stderr)

	gapsFile := fs.String("gaps", "", "path to a JSON file containing []patches.Gap (required)")
	service := fs.String("service", "", "service name for risks scoping (required)")
	model := fs.String("model", "", "LLM model id (default: ORION_LLM_MODEL env)")
	seed := fs.Int64("seed", 0, "deterministic LLM seed (recorded on each patch)")

	fs.Usage = func() {
		_, _ = fmt.Fprintf(c.stderr, "Usage: %s synth --gaps=<file> --service=<name> [--model=<id>] [--seed=N]\n\n", progName)
		_, _ = fmt.Fprintln(c.stderr, "Required environment:")
		_, _ = fmt.Fprintln(c.stderr, "  POLARIS_BASE_URL  Polaris API base (e.g. http://localhost:8080)")
		_, _ = fmt.Fprintln(c.stderr, "  POLARIS_API_KEY   Polaris bearer token")
		_, _ = fmt.Fprintln(c.stderr, "  GOOGLE_CLOUD_PROJECT, GOOGLE_CLOUD_LOCATION  Vertex AI auth")
		_, _ = fmt.Fprintln(c.stderr, "")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *gapsFile == "" || *service == "" {
		_, _ = fmt.Fprintln(c.stderr, "error: --gaps and --service are required")
		fs.Usage()
		return 2
	}

	gaps, err := loadGaps(*gapsFile)
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "error: %v\n", err)
		return 2
	}

	pcli, err := polarisFromEnv()
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "error: %v\n", err)
		return 3
	}
	catalog, err := pcli.ListControls(ctx, polaris.ListControlsOptions{Limit: 200})
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "error: snapshot controls: %v\n", err)
		return 3
	}

	gen, closer, err := llmFromEnv(ctx, *model)
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "error: %v\n", err)
		return 3
	}
	if closer != nil {
		defer closer()
	}

	builder := enrichment.NewBuilder(pcli, catalog)
	synth := patches.NewSynthesizer(gen, *model, *seed)

	allPatches := make([]patches.CandidatePatch, 0, len(gaps))
	allErrs := make([]string, 0)
	for _, gap := range gaps {
		block, berr := builder.Build(ctx, enrichment.Query{
			IssueID: gap.ID,
			Pattern: string(gap.Pattern),
			Service: *service,
		})
		if berr != nil {
			allErrs = append(allErrs, fmt.Sprintf("gap %s: enrichment: %v", gap.ID, berr))
			continue
		}
		ps, perrs := synth.Synthesize(ctx, []patches.Gap{gap}, block, patches.SynthesizeOptions{})
		for i, p := range ps {
			if perrs[i] != nil {
				allErrs = append(allErrs, fmt.Sprintf("gap %s: %v", gap.ID, perrs[i]))
				continue
			}
			allPatches = append(allPatches, p)
		}
	}

	enc := json.NewEncoder(c.stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{
		"patches": allPatches,
		"errors":  allErrs,
	}); err != nil {
		_, _ = fmt.Fprintf(c.stderr, "error: encode: %v\n", err)
		return 1
	}
	if len(allErrs) > 0 && len(allPatches) == 0 {
		return 4
	}
	return 0
}

func loadGaps(path string) ([]patches.Gap, error) {
	body, err := os.ReadFile(path) //#nosec G304 -- path is operator-supplied
	if err != nil {
		return nil, fmt.Errorf("read gaps: %w", err)
	}
	var gaps []patches.Gap
	if err := json.Unmarshal(body, &gaps); err != nil {
		return nil, fmt.Errorf("decode gaps: %w", err)
	}
	if len(gaps) == 0 {
		return nil, errors.New("gaps file empty")
	}
	return gaps, nil
}

// polarisFromEnv builds a polaris.Client from POLARIS_BASE_URL +
// POLARIS_API_KEY env vars.
func polarisFromEnv() (*polaris.Client, error) {
	base := os.Getenv("POLARIS_BASE_URL")
	key := os.Getenv("POLARIS_API_KEY")
	if base == "" || key == "" {
		return nil, errors.New("POLARIS_BASE_URL and POLARIS_API_KEY required")
	}
	return polaris.NewClient(polaris.Config{BaseURL: base, APIKey: key})
}

// llmFromEnv builds an llm.Generator from environment vars. Returns
// the generator, an optional close func (nil when none needed), or an
// error.
func llmFromEnv(ctx context.Context, model string) (llm.Generator, func(), error) {
	cfg, err := llm.LoadFromEnv()
	if err != nil {
		return nil, nil, err
	}
	if model != "" {
		cfg.Model = model
	}
	c, err := llm.NewClient(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return c, func() { _ = c.Close() }, nil
}
