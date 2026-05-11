package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/backlog"
	"github.com/revelara-ai/orion/internal/database"
	"github.com/revelara-ai/orion/internal/repos"
	"github.com/revelara-ai/orion/internal/trackers"

	// Blank-import the adapters to register their factories with
	// internal/trackers. Without these, NewByKind returns
	// FactoryError for github_issues / linear.
	_ "github.com/revelara-ai/orion/internal/trackers/github"
	_ "github.com/revelara-ai/orion/internal/trackers/linear"
)

// backlogCmd implements `orion-cli backlog <ingest|list>`.
//
// Subcommands:
//
//	ingest --binding=<uuid> --org=<uuid>
//	list   --binding=<uuid> --org=<uuid>
//
// Reads POSTGRES_DSN from the env. The --org flag seeds RLS context
// (no auth middleware in CLI context); the binding lookup is then
// scoped to that org via the standard RLSPool path.
type backlogCmd struct {
	stdout io.Writer
	stderr io.Writer
}

func newBacklogCmd(stdout, stderr io.Writer) *backlogCmd {
	return &backlogCmd{stdout: stdout, stderr: stderr}
}

func (c *backlogCmd) Name() string { return "backlog" }

func (c *backlogCmd) Synopsis() string {
	return "Manage backlog ingestion (ingest, list)"
}

func (c *backlogCmd) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		c.printSubcommandUsage()
		return 2
	}
	switch args[0] {
	case "ingest":
		return c.runIngest(ctx, args[1:])
	case "list":
		return c.runList(ctx, args[1:])
	case "-h", "--help", "help":
		c.printSubcommandUsage()
		return 0
	default:
		_, _ = fmt.Fprintf(c.stderr, "backlog: unknown subcommand %q\n\n", args[0])
		c.printSubcommandUsage()
		return 2
	}
}

func (c *backlogCmd) printSubcommandUsage() {
	_, _ = fmt.Fprintln(c.stderr, "Usage: orion-cli backlog <ingest|list> [flags]")
	_, _ = fmt.Fprintln(c.stderr, "")
	_, _ = fmt.Fprintln(c.stderr, "Required environment: POSTGRES_DSN (e.g. postgres://user:pw@host:5432/orion?sslmode=disable)")
}

func (c *backlogCmd) runIngest(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("backlog ingest", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	bindingStr := fs.String("binding", "", "tracker_binding UUID (required)")
	orgStr := fs.String("org", "", "organization UUID for RLS context (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	bindingID, orgID, code := parseBindingAndOrg(*bindingStr, *orgStr, c.stderr)
	if code != 0 {
		return code
	}

	driver, cleanup, code := c.buildDriver(ctx)
	if code != 0 {
		return code
	}
	defer cleanup()

	rlsCtx := database.WithRLSContext(ctx, "orion-cli", orgID, nil)
	res, err := driver.IngestBinding(rlsCtx, bindingID)
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "backlog ingest: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(c.stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		_, _ = fmt.Fprintf(c.stderr, "encode result: %v\n", err)
		return 1
	}
	return 0
}

func (c *backlogCmd) runList(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("backlog list", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	bindingStr := fs.String("binding", "", "tracker_binding UUID (required)")
	orgStr := fs.String("org", "", "organization UUID for RLS context (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	bindingID, orgID, code := parseBindingAndOrg(*bindingStr, *orgStr, c.stderr)
	if code != 0 {
		return code
	}

	pool, rls, cleanup, code := c.buildPool(ctx)
	if code != 0 {
		return code
	}
	defer cleanup()
	_ = pool

	rlsCtx := database.WithRLSContext(ctx, "orion-cli", orgID, nil)
	binding, err := repos.NewTrackerBindingRepo(rls).Get(rlsCtx, bindingID)
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "get binding: %v\n", err)
		return 1
	}
	issues, err := repos.NewNormalizedIssueRepo(rls).ListByRepo(rlsCtx, binding.RepoID, repos.ListByRepoOptions{})
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "list issues: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(c.stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(issues); err != nil {
		_, _ = fmt.Fprintf(c.stderr, "encode issues: %v\n", err)
		return 1
	}
	return 0
}

// buildDriver constructs a fully-wired backlog.Driver against the
// process's POSTGRES_DSN. Returns (driver, cleanup, exit-code). When
// exit-code != 0, the driver is nil and cleanup is a no-op.
func (c *backlogCmd) buildDriver(ctx context.Context) (*backlog.Driver, func(), int) {
	_, rls, cleanup, code := c.buildPool(ctx)
	if code != 0 {
		return nil, func() {}, code
	}
	driver := &backlog.Driver{
		Bindings:       repos.NewTrackerBindingRepo(rls),
		Repos:          repos.NewConnectedRepoRepo(rls),
		Issues:         repos.NewNormalizedIssueRepo(rls),
		AdapterFactory: trackers.NewByKind,
		ResolveCredentials: func(_ context.Context, binding repos.TrackerBinding) (trackers.Credentials, error) {
			// v1: GitHub adapters read app_id/installation_id/
			// private_key_pem from binding.Config (no separate
			// credential row needed). Linear OAuth credential
			// resolution is deferred per orion-13j.
			return trackers.Credentials{}, nil
		},
	}
	return driver, cleanup, 0
}

// buildPool reads POSTGRES_DSN, opens a pool, runs migrations, and
// returns the raw pool + RLS-wrapped pool + cleanup.
func (c *backlogCmd) buildPool(ctx context.Context) (*database.Pool, *database.RLSPool, func(), int) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		_, _ = fmt.Fprintln(c.stderr, "error: POSTGRES_DSN env var required")
		return nil, nil, func() {}, 3
	}
	pool, err := database.NewPool(ctx, dsn)
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "open pool: %v\n", err)
		return nil, nil, func() {}, 3
	}
	cleanup := func() { pool.Close() }
	if err := database.Migrate(ctx, pool); err != nil {
		cleanup()
		_, _ = fmt.Fprintf(c.stderr, "migrate: %v\n", err)
		return nil, nil, func() {}, 3
	}
	rls := database.NewRLSPool(pool)
	return pool, rls, cleanup, 0
}

func parseBindingAndOrg(bindingStr, orgStr string, stderr io.Writer) (uuid.UUID, uuid.UUID, int) {
	if bindingStr == "" || orgStr == "" {
		_, _ = fmt.Fprintln(stderr, "error: --binding and --org are required")
		return uuid.Nil, uuid.Nil, 2
	}
	bindingID, err := uuid.Parse(bindingStr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "invalid --binding uuid: %v\n", err)
		return uuid.Nil, uuid.Nil, 2
	}
	orgID, err := uuid.Parse(orgStr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "invalid --org uuid: %v\n", err)
		return uuid.Nil, uuid.Nil, 2
	}
	return bindingID, orgID, 0
}
