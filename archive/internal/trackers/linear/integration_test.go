//go:build integration
// +build integration

package linear

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/trackers"
)

// Live integration test. Skips cleanly when ORION_LINEAR_* env vars
// are absent so CI runs that don't have Linear credentials don't
// fail.
//
// Env vars (all required for the test to run):
//   - ORION_LINEAR_CLIENT_ID
//   - ORION_LINEAR_CLIENT_SECRET
//   - ORION_LINEAR_ACCESS_TOKEN
//   - ORION_LINEAR_REFRESH_TOKEN
//   - ORION_LINEAR_WORKSPACE_SLUG
//   - ORION_LINEAR_TEAM_ID
//
// Run with: go test -tags=integration ./internal/trackers/linear/...
func TestLinearLive_HealthCheck(t *testing.T) {
	binding := liveBinding(t)
	a := NewAdapter()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.HealthCheck(ctx, binding); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
}

func TestLinearLive_FetchCandidates(t *testing.T) {
	binding := liveBinding(t)
	a := NewAdapter()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	got, err := a.FetchCandidates(ctx, binding, time.Time{})
	if err != nil {
		t.Fatalf("FetchCandidates: %v", err)
	}
	t.Logf("got %d issues from live Linear", len(got))
}

func liveBinding(t *testing.T) trackers.TrackerBinding {
	t.Helper()
	required := map[string]string{}
	for _, key := range []string{
		"ORION_LINEAR_CLIENT_ID",
		"ORION_LINEAR_CLIENT_SECRET",
		"ORION_LINEAR_ACCESS_TOKEN",
		"ORION_LINEAR_REFRESH_TOKEN",
		"ORION_LINEAR_WORKSPACE_SLUG",
		"ORION_LINEAR_TEAM_ID",
	} {
		v := os.Getenv(key)
		if v == "" {
			t.Skipf("%s not set; skipping live linear test", key)
		}
		required[key] = v
	}
	return trackers.TrackerBinding{
		Kind: trackers.TrackerKindLinear,
		Config: map[string]any{
			"workspace_slug": required["ORION_LINEAR_WORKSPACE_SLUG"],
			"team_id":        required["ORION_LINEAR_TEAM_ID"],
		},
		Credentials: trackers.Credentials{
			OAuth2AccessToken:  required["ORION_LINEAR_ACCESS_TOKEN"],
			OAuth2RefreshToken: required["ORION_LINEAR_REFRESH_TOKEN"],
			Extra: map[string]string{
				"client_id":     required["ORION_LINEAR_CLIENT_ID"],
				"client_secret": required["ORION_LINEAR_CLIENT_SECRET"],
			},
		},
	}
}
