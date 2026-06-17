package conformance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/revelara-ai/orion/internal/trackers"
)

// noOpAdapter is a stub TrackerAdapter used by the suite's self-test
// to prove the harness wires up end-to-end without any real provider
// in scope. Each real adapter (E2-2 GitHub, E2-4 Linear) ships its
// own _test.go that calls Run with a similar factory.
type noOpAdapter struct {
	fixture Fixture
}

func (n *noOpAdapter) Kind() trackers.TrackerKind {
	return trackers.TrackerKindGitHubIssues // arbitrary; suite accepts any v1 enum
}

func (n *noOpAdapter) FetchCandidates(_ context.Context, _ trackers.TrackerBinding, since time.Time) ([]trackers.NormalizedIssue, error) {
	out := make([]trackers.NormalizedIssue, 0, len(n.fixture.Issues))
	for _, i := range n.fixture.Issues {
		if !since.IsZero() && i.LastUpdated.Before(since) {
			continue
		}
		out = append(out, i)
	}
	return out, nil
}

func (n *noOpAdapter) FetchByExternalIDs(_ context.Context, _ trackers.TrackerBinding, ids []string) ([]trackers.NormalizedIssue, error) {
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	out := make([]trackers.NormalizedIssue, 0, len(ids))
	for _, i := range n.fixture.Issues {
		if _, ok := want[i.ExternalID]; ok {
			out = append(out, i)
		}
	}
	return out, nil
}

func (n *noOpAdapter) Create(_ context.Context, _ trackers.TrackerBinding, draft trackers.IssueDraft) (trackers.NormalizedIssue, error) {
	return trackers.NormalizedIssue{
		ExternalID:  "stub:org/repo#999",
		ExternalURL: "https://example.test/stub/org/repo/issues/999",
		Title:       draft.Title,
		Description: draft.Body,
		Labels:      draft.Labels,
		State:       trackers.StateOpen,
		LastUpdated: time.Now().UTC(),
	}, nil
}

func (n *noOpAdapter) UpdateState(_ context.Context, _ trackers.TrackerBinding, _ string, _ trackers.NormalizedState) error {
	return nil
}

func (n *noOpAdapter) Comment(_ context.Context, _ trackers.TrackerBinding, _, _ string) error {
	return nil
}

func (n *noOpAdapter) Capabilities() trackers.TrackerCapabilities {
	return trackers.TrackerCapabilities{
		CanCreate:           true,
		CanUpdateState:      true,
		CanComment:          true,
		SupportsLabelFilter: false,
		SupportsSince:       true,
	}
}

func (n *noOpAdapter) HealthCheck(_ context.Context, _ trackers.TrackerBinding) error {
	return nil
}

// TestConformance_NoOpAdapter proves the suite is runnable. Real
// adapters (E2-2 GitHub, E2-4 Linear) ship their own TestConformance
// in their own _test.go.
func TestConformance_NoOpAdapter(t *testing.T) {
	Run(t, SuiteOptions{
		Factory: func(_ *testing.T) (trackers.TrackerAdapter, trackers.TrackerBinding, Fixture) {
			fx := DefaultFixture()
			return &noOpAdapter{fixture: fx}, trackers.TrackerBinding{
				ID:    uuid.New(),
				OrgID: uuid.New(),
				Kind:  trackers.TrackerKindGitHubIssues,
				Credentials: trackers.Credentials{
					AppToken: "stub-token",
				},
			}, fx
		},
	})
}

// failingHealthAdapter helps cover the "binding failed health check"
// branch. The conformance suite itself doesn't currently assert this
// branch (an adapter that fails health is just unhappy, not broken),
// so this test stands alone to document the contract callers (E2-6
// ingestion) rely on: HealthCheck failures are surfaced as wrapped
// sentinels.
type failingHealthAdapter struct {
	noOpAdapter
}

func (f *failingHealthAdapter) HealthCheck(_ context.Context, _ trackers.TrackerBinding) error {
	return trackers.ErrUnauthenticated
}

func TestHealthCheckFailureSurfaced(t *testing.T) {
	a := &failingHealthAdapter{noOpAdapter: noOpAdapter{fixture: DefaultFixture()}}
	err := a.HealthCheck(context.Background(), trackers.TrackerBinding{})
	if !errors.Is(err, trackers.ErrUnauthenticated) {
		t.Errorf("HealthCheck err = %v, want ErrUnauthenticated", err)
	}
}
