package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/revelara-ai/orion/internal/architect"
	"github.com/revelara-ai/orion/internal/constraints"
	"github.com/revelara-ai/orion/internal/detection"
	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/orchestration"
	"github.com/revelara-ai/orion/internal/polaris"
)

// runCmd implements `orion-cli run --repo=... --service=... [--dry-run]`.
// In v1 this is the single-process "bullet" entry point: detect →
// architect → constraints → enrich → synthesize → harness → verify →
// compose → report. The actual PR open requires a configured GitHub
// App; without it, --dry-run is the verifiable path.
type runCmd struct {
	stdout io.Writer
	stderr io.Writer
}

func newRunCmd(stdout, stderr io.Writer) *runCmd {
	return &runCmd{stdout: stdout, stderr: stderr}
}

func (c *runCmd) Name() string { return "run" }

func (c *runCmd) Synopsis() string {
	return "Run the full Orion pipeline against a repo (detect → patch → verify → PR plan)"
}

func (c *runCmd) Run(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(c.stderr)

	repoPath := fs.String("repo", "", "absolute path to a writable working tree (required)")
	service := fs.String("service", "", "service name within the repo (required)")
	runID := fs.String("run-id", "", "run identifier (default: random)")
	seed := fs.Int64("seed", 1, "deterministic seed for harness + bootstrap")
	llmModel := fs.String("model", "", "LLM model id (default: ORION_LLM_MODEL env)")
	llmSeed := fs.Int64("llm-seed", 0, "LLM provider seed (0 = unspecified)")
	patchedDelta := fs.Float64("patched-delta", 0.5, "v1 InProcessRunner improvement factor (deferred to live K8s runner)")
	rvlBinary := fs.String("rvl-binary", "", "path to rvl binary (default: 'rvl' in $PATH)")
	dryRun := fs.Bool("dry-run", true, "emit PRPlan as JSON without opening a PR (default true; live PR opening lands when GitHub App is wired into this subcommand)")
	issueTitle := fs.String("issue-title", "Reliability remediation", "tracker issue title (used in PR title)")
	issueExternalID := fs.String("issue-id", "", "tracker external_id (used in branch name)")

	fs.Usage = func() {
		_, _ = fmt.Fprintf(c.stderr, "Usage: %s run --repo=<path> --service=<name> [flags]\n\n", progName)
		_, _ = fmt.Fprintln(c.stderr, "Required environment:")
		_, _ = fmt.Fprintln(c.stderr, "  POLARIS_BASE_URL  Polaris API base")
		_, _ = fmt.Fprintln(c.stderr, "  POLARIS_API_KEY   Polaris bearer token")
		_, _ = fmt.Fprintln(c.stderr, "  GOOGLE_CLOUD_PROJECT, GOOGLE_CLOUD_LOCATION  Vertex AI auth")
		_, _ = fmt.Fprintln(c.stderr, "")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *repoPath == "" || *service == "" {
		_, _ = fmt.Fprintln(c.stderr, "error: --repo and --service are required")
		fs.Usage()
		return 2
	}
	rid := *runID
	if rid == "" {
		rid = randHexShort()
	}

	pcli, err := polarisFromEnv()
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "error: %v\n", err)
		return 3
	}

	gen, closer, err := llmFromEnv(ctx, *llmModel)
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "error: %v\n", err)
		return 3
	}
	if closer != nil {
		defer closer()
	}

	scanner := detection.NewScanner(detection.ScannerConfig{RvlBinary: *rvlBinary})
	arch := architect.NewInferer(architect.InfererConfig{Generator: gen})
	constraintInferer := constraints.NewInferer()

	pipeline := &orchestration.Pipeline{
		Polaris:           pcli,
		Enrichment:        pcli,
		LLM:               gen,
		LLMModel:          *llmModel,
		LLMSeed:           *llmSeed,
		Scanner:           scanner,
		Architect:         arch,
		ConstraintInferer: constraintInferer,
		PatchedDelta:      *patchedDelta,
		Trace: func(stage orchestration.Stage, msg string) {
			_, _ = fmt.Fprintf(c.stderr, "[%s] %s\n", stage, msg)
		},
	}
	res, err := pipeline.Run(ctx, orchestration.RunOptions{
		RunID:    rid,
		RepoPath: *repoPath,
		Service:  *service,
		Issue:    orchestration.Issue{Title: *issueTitle, ExternalID: *issueExternalID},
		Seed:     *seed,
	})
	if err != nil && !errors.Is(err, orchestration.ErrNoImprovement) {
		_, _ = fmt.Fprintf(c.stderr, "error: %v\n", err)
		return 1
	}

	if errors.Is(err, orchestration.ErrNoImprovement) {
		_, _ = fmt.Fprintln(c.stderr, "no_improvement: no patches accepted")
		// Still emit the result envelope so callers can inspect findings/rejections.
	}

	if !*dryRun {
		_, _ = fmt.Fprintln(c.stderr, "warning: --dry-run=false but live PR opening is not yet wired in this subcommand; emitting plan only")
	}

	// Emit the run result + PR plan as JSON to stdout.
	enc := json.NewEncoder(c.stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		_, _ = fmt.Fprintf(c.stderr, "error: encode: %v\n", err)
		return 1
	}
	if errors.Is(err, orchestration.ErrNoImprovement) {
		return 5
	}
	return 0
}

// polaris.Client must satisfy orchestration.PolarisCatalogReader.
// Compile-time check (not a runtime helper) — if polaris.Client's
// signature drifts, this fails at build time.
var _ orchestration.PolarisCatalogReader = (*polaris.Client)(nil)

// polaris.Client must satisfy orchestration.PolarisEnrichmentReader
// (which is a type alias for enrichment.PolarisReader).
// We don't add a compile-time check because PolarisEnrichmentReader
// is a type alias and the assertion would be circular through the
// type system; the build itself catches mismatches at the call site.

// llmFromEnv is shared with synth.go; declared once there.
// polarisFromEnv similarly. randHexShort similarly.

// Ensure orchestration.Stage is used somewhere (avoid unused import).
var _ = llm.GenerateRequest{}
