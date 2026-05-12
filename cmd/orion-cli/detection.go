package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/database"
	"github.com/revelara-ai/orion/internal/detection"
	"github.com/revelara-ai/orion/internal/repos"
)

// detectionCmd implements `orion-cli detection <tick>`.
//
// Subcommands:
//
//	tick --binding=<uuid> --org=<uuid> --repo-path=<path> --service=<name> [--mode=full|incremental|post_merge]
//
// The tick subcommand runs one LoopDriver.Tick against the operator-
// provided checkout at --repo-path. v1 ships without the scheduler
// (E3-3) and without progressive-disclosure cap (E3-6); those slices
// drive the loop differently. This subcommand is the dogfood path so
// operators can validate the persistence + dedup wiring end-to-end.
type detectionCmd struct {
	stdout io.Writer
	stderr io.Writer
}

func newDetectionCmd(stdout, stderr io.Writer) *detectionCmd {
	return &detectionCmd{stdout: stdout, stderr: stderr}
}

func (c *detectionCmd) Name() string { return "detection" }

func (c *detectionCmd) Synopsis() string {
	return "Run one detection tick against a binding (tick)"
}

func (c *detectionCmd) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		c.printUsage()
		return 2
	}
	switch args[0] {
	case "tick":
		return c.runTick(ctx, args[1:])
	case "-h", "--help", "help":
		c.printUsage()
		return 0
	default:
		_, _ = fmt.Fprintf(c.stderr, "detection: unknown subcommand %q\n\n", args[0])
		c.printUsage()
		return 2
	}
}

func (c *detectionCmd) printUsage() {
	_, _ = fmt.Fprintln(c.stderr, "Usage: orion-cli detection tick --binding=<uuid> --org=<uuid> --repo-path=<path> --service=<name> [--mode=full]")
	_, _ = fmt.Fprintln(c.stderr, "")
	_, _ = fmt.Fprintln(c.stderr, "Required env: POSTGRES_DSN")
}

func (c *detectionCmd) runTick(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("detection tick", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	bindingStr := fs.String("binding", "", "tracker_binding UUID (required)")
	orgStr := fs.String("org", "", "organization UUID for RLS context (required)")
	repoPath := fs.String("repo-path", "", "absolute path to the checked-out repo (required)")
	service := fs.String("service", "", "rvl-cli --service name (required)")
	modeStr := fs.String("mode", "full", "loop mode: full|incremental|post_merge")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *bindingStr == "" || *orgStr == "" || *repoPath == "" || *service == "" {
		_, _ = fmt.Fprintln(c.stderr, "error: --binding, --org, --repo-path, --service all required")
		return 2
	}
	bindingID, err := uuid.Parse(*bindingStr)
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "binding: %v\n", err)
		return 2
	}
	orgID, err := uuid.Parse(*orgStr)
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "org: %v\n", err)
		return 2
	}

	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		_, _ = fmt.Fprintln(c.stderr, "error: POSTGRES_DSN env var required")
		return 3
	}
	pool, err := database.NewPool(ctx, dsn)
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "open pool: %v\n", err)
		return 3
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		_, _ = fmt.Fprintf(c.stderr, "migrate: %v\n", err)
		return 3
	}
	rls := database.NewRLSPool(pool)
	rlsCtx := database.WithRLSContext(ctx, "orion-cli", orgID, nil)

	binding, err := repos.NewTrackerBindingRepo(rls).Get(rlsCtx, bindingID)
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "get binding: %v\n", err)
		return 1
	}

	scanner := detection.NewScanner(detection.ScannerConfig{})
	driver := detection.NewLoopDriver(
		scanner,
		repos.NewDetectionRunRepo(rls),
		repos.NewDetectionFindingRepo(rls),
		repos.NewNormalizedIssueRepo(rls),
		nil, // AutoFileGate wiring deferred to E3-5/E3-6 (risksink + cap)
	)

	res, err := driver.Tick(rlsCtx, detection.LoopInput{
		BindingID: binding.ID.String(),
		RepoPath:  *repoPath,
		Service:   *service,
		Mode:      detection.LoopMode(*modeStr),
		AutoFile:  binding.AutoFile,
	})
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "tick: %v\n", err)
		// Still print the partial result if one was returned (failed run row).
		if res.RunID != "" {
			enc := json.NewEncoder(c.stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(res)
		}
		return 1
	}

	enc := json.NewEncoder(c.stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		_, _ = fmt.Fprintf(c.stderr, "encode: %v\n", err)
		return 1
	}
	return 0
}
