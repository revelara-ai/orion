//go:build integration

// Live integration test for the GitHub Issues adapter. Run with:
//
//	go test -tags=integration ./internal/trackers/github/... \
//	  -run TestLiveAgainstFixture -timeout=5m
//
// Requires (same shape as E1-1):
//
//	ORION_GITHUB_APP_ID, ORION_GITHUB_INSTALLATION_ID,
//	ORION_GITHUB_PRIVATE_KEY (PEM), ORION_GITHUB_FIXTURE_OWNER,
//	ORION_GITHUB_FIXTURE_REPO
//
// Skips cleanly when any env var is missing. Asserts the adapter
// can FetchCandidates against a live repo and HealthCheck succeeds.
package github

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	gh "github.com/revelara-ai/orion/internal/github"
	"github.com/revelara-ai/orion/internal/trackers"
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

func TestLiveAgainstFixture(t *testing.T) {
	appID := envInt64(t, "ORION_GITHUB_APP_ID")
	instID := envInt64(t, "ORION_GITHUB_INSTALLATION_ID")
	pemKey := envOrSkip(t, "ORION_GITHUB_PRIVATE_KEY")
	owner := envOrSkip(t, "ORION_GITHUB_FIXTURE_OWNER")
	repo := envOrSkip(t, "ORION_GITHUB_FIXTURE_REPO")

	adapter := NewAdapterWithFactory(func(b trackers.TrackerBinding) (*gh.App, error) {
		return gh.NewApp(gh.AppConfig{
			AppID:          appID,
			InstallationID: instID,
			PrivateKeyPEM:  []byte(pemKey),
		})
	})
	binding := trackers.TrackerBinding{
		ID:    uuid.New(),
		OrgID: uuid.New(),
		Kind:  trackers.TrackerKindGitHubIssues,
		Config: map[string]any{
			"repo_full_name": owner + "/" + repo,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	if err := adapter.HealthCheck(ctx, binding); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}

	issues, err := adapter.FetchCandidates(ctx, binding, time.Time{})
	if err != nil {
		t.Fatalf("FetchCandidates: %v", err)
	}
	t.Logf("fetched %d issues from %s/%s", len(issues), owner, repo)
	for _, i := range issues {
		if i.ExternalID == "" {
			t.Errorf("issue with empty ExternalID: %+v", i)
		}
	}
}
