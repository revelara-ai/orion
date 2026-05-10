package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/revelara-ai/orion/internal/github"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// roundtripCmd implements `orion-cli roundtrip --repo=<https-url>` for
// proving the GitHub App round-trip end-to-end. Reads App credentials
// from environment variables (NEVER from .revelara.yaml per SPEC §10.4).
type roundtripCmd struct {
	stdout io.Writer
	stderr io.Writer
}

func newRoundtripCmd(stdout, stderr io.Writer) *roundtripCmd {
	return &roundtripCmd{stdout: stdout, stderr: stderr}
}

func (c *roundtripCmd) Name() string { return "roundtrip" }

func (c *roundtripCmd) Synopsis() string {
	return "GitHub App round-trip smoke test against a fixture repo"
}

func (c *roundtripCmd) Run(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("roundtrip", flag.ContinueOnError)
	fs.SetOutput(c.stderr)

	repoURL := fs.String("repo", "", "https URL of the target fixture repo (required)")
	base := fs.String("base", "main", "base branch for the PR")
	sandboxRoot := fs.String("sandbox-root", "", "absolute path for sandbox workspaces (default: ORION_SANDBOX_ROOT or os.TempDir)")

	fs.Usage = func() {
		_, _ = fmt.Fprintf(c.stderr, "Usage: %s roundtrip --repo=<https-url> [--base=main] [--sandbox-root=<path>]\n\n", progName)
		_, _ = fmt.Fprintln(c.stderr, "Required environment:")
		_, _ = fmt.Fprintln(c.stderr, "  ORION_GITHUB_APP_ID            integer App ID")
		_, _ = fmt.Fprintln(c.stderr, "  ORION_GITHUB_INSTALLATION_ID   integer install ID against the fixture")
		_, _ = fmt.Fprintln(c.stderr, "  ORION_GITHUB_PRIVATE_KEY       PEM-encoded RSA private key")
		_, _ = fmt.Fprintln(c.stderr, "")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *repoURL == "" {
		_, _ = fmt.Fprintln(c.stderr, "error: --repo is required")
		fs.Usage()
		return 2
	}
	owner, repo, err := parseOwnerRepo(*repoURL)
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "error: %v\n", err)
		return 2
	}
	if isUpstreamForbidden(owner) {
		_, _ = fmt.Fprintf(c.stderr, "error: refusing to operate against upstream owner %q (SPEC safety guard)\n", owner)
		return 20
	}

	app, err := loadAppFromEnv()
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "error: %v\n", err)
		return 2
	}

	root := *sandboxRoot
	if root == "" {
		root = os.Getenv("ORION_SANDBOX_ROOT")
	}
	if root == "" {
		root = os.TempDir()
	}
	if !filepath.IsAbs(root) {
		_, _ = fmt.Fprintf(c.stderr, "error: sandbox root must be absolute, got %q\n", root)
		return 2
	}

	short := randHexShort()
	wsKey := "rt-" + short

	rc := 0
	err = sandbox.RunWithCleanup(sandbox.Options{Root: root, Key: wsKey}, func(ws *sandbox.Workspace) error {
		token, err := app.InstallationToken(ctx)
		if err != nil {
			return fmt.Errorf("installation token: %w", err)
		}
		repoDir := filepath.Join(ws.Path, "repo")
		if err := github.Clone(ctx, github.CloneOptions{
			RepoURL: *repoURL,
			Token:   token,
			Dest:    repoDir,
			Depth:   1,
		}); err != nil {
			return fmt.Errorf("clone: %w", err)
		}
		branch := github.BranchName(short, "manual-roundtrip")
		if err := github.CommitAndPush(ctx, token, github.CommitOptions{
			RepoDir:     repoDir,
			BranchName:  branch,
			AuthorName:  "orion[bot]",
			AuthorEmail: "orion[bot]@users.noreply.github.com",
			Message:     "[orion] roundtrip smoke",
			Files:       map[string]string{".orion-roundtrip.txt": "Hello Orion " + short + "\n"},
		}); err != nil {
			return fmt.Errorf("commit and push: %w", err)
		}
		pr, err := app.CreatePR(ctx, github.PROptions{
			Owner: owner, Repo: repo,
			Head: branch, Base: *base,
			Title: "[orion] roundtrip smoke " + short,
			Body:  "Automated round-trip smoke. Safe to close.",
			Draft: true,
		})
		if err != nil {
			return fmt.Errorf("create PR: %w", err)
		}
		_, _ = fmt.Fprintf(c.stdout, "opened PR #%d at %s\n", pr.Number, pr.HTMLURL)
		if _, err := app.PostComment(ctx, owner, repo, pr.Number, "Roundtrip ack from orion-cli."); err != nil {
			return fmt.Errorf("post comment: %w", err)
		}
		_, _ = fmt.Fprintln(c.stdout, "posted comment, workspace will be cleaned up")
		return nil
	})
	if err != nil {
		_, _ = fmt.Fprintf(c.stderr, "error: %v\n", err)
		rc = 1
	}
	return rc
}

// loadAppFromEnv reads ORION_GITHUB_* env vars and returns a configured App.
func loadAppFromEnv() (*github.App, error) {
	appIDStr := os.Getenv("ORION_GITHUB_APP_ID")
	instIDStr := os.Getenv("ORION_GITHUB_INSTALLATION_ID")
	pem := os.Getenv("ORION_GITHUB_PRIVATE_KEY")
	if appIDStr == "" || instIDStr == "" || pem == "" {
		return nil, errors.New("ORION_GITHUB_APP_ID, ORION_GITHUB_INSTALLATION_ID, ORION_GITHUB_PRIVATE_KEY must be set")
	}
	appID, err := strconv.ParseInt(appIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("ORION_GITHUB_APP_ID: not an int64: %w", err)
	}
	instID, err := strconv.ParseInt(instIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("ORION_GITHUB_INSTALLATION_ID: not an int64: %w", err)
	}
	return github.NewApp(github.AppConfig{AppID: appID, InstallationID: instID, PrivateKeyPEM: []byte(pem)})
}

// parseOwnerRepo extracts owner and repo name from an https GitHub URL.
func parseOwnerRepo(raw string) (string, string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("parse repo URL: %w", err)
	}
	if u.Scheme != "https" {
		return "", "", fmt.Errorf("repo URL must be https, got %q", u.Scheme)
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repo URL %q must have /<owner>/<repo>", raw)
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), nil
}

// isUpstreamForbidden enforces the docs/fixtures/README.md hard rule:
// never operate against the GoogleCloudPlatform upstream of the
// microservices-demo fixture (or any owner explicitly added here).
func isUpstreamForbidden(owner string) bool {
	switch strings.ToLower(owner) {
	case "googlecloudplatform":
		return true
	}
	return false
}

func randHexShort() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
