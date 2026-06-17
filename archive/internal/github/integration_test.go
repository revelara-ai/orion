//go:build integration

// Build-tag-gated live integration test. Run with:
//
//	go test -tags=integration ./internal/github/... \
//	  -run TestRoundtripLive -timeout=5m
//
// Requires:
//
//	ORION_GITHUB_APP_ID            integer App ID
//	ORION_GITHUB_INSTALLATION_ID   integer install ID against fixture repo
//	ORION_GITHUB_PRIVATE_KEY       PEM-encoded RSA private key (full body)
//	ORION_GITHUB_FIXTURE_OWNER     repo owner (e.g. "revelara-ai")
//	ORION_GITHUB_FIXTURE_REPO      repo name (e.g. "microservices-demo")
//	ORION_GITHUB_FIXTURE_BASE      base branch (default "main")
//
// The test mints an installation token, opens a draft PR with one
// hardcoded commit on a fresh orion/* branch, posts a hardcoded
// comment, and tears down the workspace. PR cleanup (close + delete
// branch) is the test's responsibility on success.
package github

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/sandbox"
)

func envOrSkip(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("integration test requires %s", key)
	}
	return v
}

func envInt64(t *testing.T, key string) int64 {
	t.Helper()
	raw := envOrSkip(t, key)
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		t.Fatalf("%s: not an int64: %v", key, err)
	}
	return n
}

func TestRoundtripLive(t *testing.T) {
	appID := envInt64(t, "ORION_GITHUB_APP_ID")
	instID := envInt64(t, "ORION_GITHUB_INSTALLATION_ID")
	pem := envOrSkip(t, "ORION_GITHUB_PRIVATE_KEY")
	owner := envOrSkip(t, "ORION_GITHUB_FIXTURE_OWNER")
	repo := envOrSkip(t, "ORION_GITHUB_FIXTURE_REPO")
	base := os.Getenv("ORION_GITHUB_FIXTURE_BASE")
	if base == "" {
		base = "main"
	}

	app, err := NewApp(AppConfig{AppID: appID, InstallationID: instID, PrivateKeyPEM: []byte(pem)})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	token, err := app.InstallationToken(ctx)
	if err != nil {
		t.Fatalf("InstallationToken: %v", err)
	}

	root, err := os.MkdirTemp("", "orion-it-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)

	short := randHex(3)
	ws, err := sandbox.Provision(sandbox.Options{Root: root, Key: "rt-" + short})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	defer ws.Cleanup()

	repoDir := ws.Path + "/repo"
	if err := Clone(ctx, CloneOptions{
		RepoURL: fmt.Sprintf("https://github.com/%s/%s.git", owner, repo),
		Token:   token,
		Dest:    repoDir,
		Depth:   1,
	}); err != nil {
		t.Fatalf("Clone: %v", err)
	}

	branch := BranchName(short, "manual-roundtrip")
	if err := CommitAndPush(ctx, token, CommitOptions{
		RepoDir:     repoDir,
		BranchName:  branch,
		AuthorName:  "orion[bot]",
		AuthorEmail: "orion[bot]@users.noreply.github.com",
		Message:     "[orion] roundtrip smoke",
		Files:       map[string]string{".orion-roundtrip.txt": "Hello Orion " + short + "\n"},
	}); err != nil {
		t.Fatalf("CommitAndPush: %v", err)
	}

	pr, err := app.CreatePR(ctx, PROptions{
		Owner: owner, Repo: repo,
		Head: branch, Base: base,
		Title: "[orion] roundtrip smoke " + short,
		Body:  "Automated round-trip integration test. Safe to close.",
		Draft: true,
	})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	t.Logf("opened PR #%d at %s", pr.Number, pr.HTMLURL)

	if _, err := app.PostComment(ctx, owner, repo, pr.Number, "Roundtrip ack from orion-rbn smoke test."); err != nil {
		t.Fatalf("PostComment: %v", err)
	}

	// Best-effort cleanup of the PR & branch so the fixture stays tidy.
	closeURL := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", DefaultAPIBaseURL, owner, repo, pr.Number)
	if err := app.doJSON(ctx, "PATCH", closeURL, map[string]any{"state": "closed"}, nil); err != nil {
		t.Logf("close PR (best-effort): %v", err)
	}
	branchURL := fmt.Sprintf("%s/repos/%s/%s/git/refs/heads/%s", DefaultAPIBaseURL, owner, repo, branch)
	if err := app.doJSON(ctx, "DELETE", branchURL, nil, nil); err != nil {
		t.Logf("delete branch (best-effort): %v", err)
	}
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
