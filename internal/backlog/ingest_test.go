package backlog

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/revelara-ai/orion/internal/database"
	"github.com/revelara-ai/orion/internal/repos"
	"github.com/revelara-ai/orion/internal/trackers"
)

// newRLSPool spins up a fresh pg container per test, runs migrations,
// and returns an RLSPool.
func newRLSPool(t *testing.T) *database.RLSPool {
	t.Helper()
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("orion_test"),
		tcpostgres.WithUsername("orion"),
		tcpostgres.WithPassword("orion"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("testcontainers postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := database.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return database.NewRLSPool(pool)
}

// stubAdapter is a fully in-memory TrackerAdapter for driver tests.
// It records the `since` it was last called with and returns a
// configurable issue set.
type stubAdapter struct {
	mu          sync.Mutex
	kind        trackers.TrackerKind
	issues      []trackers.NormalizedIssue
	failHealth  bool
	healthCalls int
	fetchCalls  int
	lastSince   time.Time
}

func (s *stubAdapter) Kind() trackers.TrackerKind { return s.kind }
func (s *stubAdapter) Capabilities() trackers.TrackerCapabilities {
	return trackers.TrackerCapabilities{CanCreate: true, CanUpdateState: true, SupportsSince: true}
}
func (s *stubAdapter) HealthCheck(_ context.Context, _ trackers.TrackerBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.healthCalls++
	if s.failHealth {
		return errors.New("stub health failure")
	}
	return nil
}
func (s *stubAdapter) FetchCandidates(_ context.Context, _ trackers.TrackerBinding, since time.Time) ([]trackers.NormalizedIssue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fetchCalls++
	s.lastSince = since
	out := make([]trackers.NormalizedIssue, 0, len(s.issues))
	for _, iss := range s.issues {
		if !since.IsZero() && iss.LastUpdated.Before(since) {
			continue
		}
		out = append(out, iss)
	}
	return out, nil
}
func (s *stubAdapter) FetchByExternalIDs(_ context.Context, _ trackers.TrackerBinding, ids []string) ([]trackers.NormalizedIssue, error) {
	return nil, nil
}
func (s *stubAdapter) Create(_ context.Context, _ trackers.TrackerBinding, _ trackers.IssueDraft) (trackers.NormalizedIssue, error) {
	return trackers.NormalizedIssue{}, errors.New("not implemented")
}
func (s *stubAdapter) UpdateState(_ context.Context, _ trackers.TrackerBinding, _ string, _ trackers.NormalizedState) error {
	return nil
}
func (s *stubAdapter) Comment(_ context.Context, _ trackers.TrackerBinding, _ string, _ string) error {
	return nil
}

func seedRepoBinding(t *testing.T, rls *database.RLSPool, ctx context.Context, repoName string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	repo, err := repos.NewConnectedRepoRepo(rls).Create(ctx, repos.ConnectedRepo{
		Provider:     "github",
		AppInstallID: "1",
		RepoFullName: repoName,
	})
	if err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	binding, err := repos.NewTrackerBindingRepo(rls).Create(ctx, repos.TrackerBinding{
		RepoID:         repo.ID,
		Kind:           repos.TrackerGitHubIssues,
		CredentialsRef: "vault://x",
	})
	if err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	return repo.ID, binding.ID
}

func driverFor(t *testing.T, rls *database.RLSPool, adapter trackers.TrackerAdapter) *Driver {
	t.Helper()
	return &Driver{
		Bindings: repos.NewTrackerBindingRepo(rls),
		Repos:    repos.NewConnectedRepoRepo(rls),
		Issues:   repos.NewNormalizedIssueRepo(rls),
		AdapterFactory: func(_ trackers.TrackerKind) (trackers.TrackerAdapter, error) {
			return adapter, nil
		},
		ResolveCredentials: func(_ context.Context, _ repos.TrackerBinding) (trackers.Credentials, error) {
			return trackers.Credentials{}, nil
		},
	}
}

func TestIngestBinding_FetchesAndUpserts(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)
	_, bindingID := seedRepoBinding(t, rls, ctx, "test/repo")

	adapter := &stubAdapter{
		kind: trackers.TrackerKindGitHubIssues,
		issues: []trackers.NormalizedIssue{
			{ExternalID: "gh:test/repo#1", Title: "one", State: trackers.StateOpen, LastUpdated: time.Now().UTC()},
			{ExternalID: "gh:test/repo#2", Title: "two", State: trackers.StateOpen, LastUpdated: time.Now().UTC()},
			{ExternalID: "gh:test/repo#3", Title: "three", State: trackers.StateInProgress, LastUpdated: time.Now().UTC()},
		},
	}
	d := driverFor(t, rls, adapter)

	res, err := d.IngestBinding(ctx, bindingID)
	if err != nil {
		t.Fatalf("IngestBinding: %v", err)
	}
	if res.IssuesFetched != 3 {
		t.Errorf("IssuesFetched=%d, want 3", res.IssuesFetched)
	}
	if res.IssuesUpserted != 3 {
		t.Errorf("IssuesUpserted=%d, want 3", res.IssuesUpserted)
	}
	if adapter.healthCalls != 1 {
		t.Errorf("healthCalls=%d, want 1", adapter.healthCalls)
	}

	got, err := repos.NewNormalizedIssueRepo(rls).GetByExternalID(ctx, "gh:test/repo#2")
	if err != nil {
		t.Fatalf("GetByExternalID: %v", err)
	}
	if got.Title != "two" {
		t.Errorf("Title=%q", got.Title)
	}
}

func TestIngestBinding_Idempotent(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)
	_, bindingID := seedRepoBinding(t, rls, ctx, "test/repo2")

	adapter := &stubAdapter{
		kind: trackers.TrackerKindGitHubIssues,
		issues: []trackers.NormalizedIssue{
			{ExternalID: "gh:test/repo2#1", Title: "one", State: trackers.StateOpen, LastUpdated: time.Now().UTC()},
		},
	}
	d := driverFor(t, rls, adapter)

	if _, err := d.IngestBinding(ctx, bindingID); err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	if _, err := d.IngestBinding(ctx, bindingID); err != nil {
		t.Fatalf("second ingest: %v", err)
	}

	list, err := repos.NewNormalizedIssueRepo(rls).ListByRepo(ctx, repoIDFromBinding(t, rls, ctx, bindingID), repos.ListByRepoOptions{})
	if err != nil {
		t.Fatalf("ListByRepo: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("got %d rows, want 1 (idempotent)", len(list))
	}
}

func TestIngestBinding_SkipsOnHealthCheckFailure(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)
	_, bindingID := seedRepoBinding(t, rls, ctx, "test/repo3")

	adapter := &stubAdapter{
		kind:       trackers.TrackerKindGitHubIssues,
		failHealth: true,
		issues: []trackers.NormalizedIssue{
			{ExternalID: "gh:test/repo3#1", Title: "one", LastUpdated: time.Now().UTC()},
		},
	}
	d := driverFor(t, rls, adapter)

	res, err := d.IngestBinding(ctx, bindingID)
	if err == nil {
		t.Fatal("expected error from failed HealthCheck")
	}
	if res.IssuesUpserted != 0 {
		t.Errorf("IssuesUpserted=%d, want 0 on health failure", res.IssuesUpserted)
	}
	if adapter.fetchCalls != 0 {
		t.Errorf("FetchCandidates should not have been called (got %d calls)", adapter.fetchCalls)
	}
}

func TestIngestBinding_HonorsSince(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)
	_, bindingID := seedRepoBinding(t, rls, ctx, "test/repo4")

	now := time.Now().UTC().Truncate(time.Second)
	adapter := &stubAdapter{
		kind: trackers.TrackerKindGitHubIssues,
		issues: []trackers.NormalizedIssue{
			{ExternalID: "gh:test/repo4#1", Title: "old", State: trackers.StateOpen, LastUpdated: now.Add(-2 * time.Hour)},
			{ExternalID: "gh:test/repo4#2", Title: "fresh", State: trackers.StateOpen, LastUpdated: now},
		},
	}
	d := driverFor(t, rls, adapter)

	if _, err := d.IngestBinding(ctx, bindingID); err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	firstSince := adapter.lastSince
	if !firstSince.IsZero() {
		t.Errorf("first call's since should be zero, got %v", firstSince)
	}

	// Second tick should pass since > zero, derived from the max
	// last_synced_at on existing rows.
	if _, err := d.IngestBinding(ctx, bindingID); err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if adapter.lastSince.IsZero() {
		t.Error("second call's since should be non-zero")
	}
}

func TestIngestBinding_MultipleBindings_OneHealthyOneFailing(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)
	_, healthyID := seedRepoBinding(t, rls, ctx, "test/healthy")
	_, brokenID := seedRepoBinding(t, rls, ctx, "test/broken")

	healthyAdapter := &stubAdapter{
		kind: trackers.TrackerKindGitHubIssues,
		issues: []trackers.NormalizedIssue{
			{ExternalID: "gh:test/healthy#1", Title: "ok", State: trackers.StateOpen, LastUpdated: time.Now().UTC()},
		},
	}
	brokenAdapter := &stubAdapter{
		kind:       trackers.TrackerKindGitHubIssues,
		failHealth: true,
	}

	healthyDriver := driverFor(t, rls, healthyAdapter)
	brokenDriver := driverFor(t, rls, brokenAdapter)

	// Healthy binding lands its issue.
	res, err := healthyDriver.IngestBinding(ctx, healthyID)
	if err != nil {
		t.Fatalf("healthy ingest: %v", err)
	}
	if res.IssuesUpserted != 1 {
		t.Errorf("healthy IssuesUpserted=%d, want 1", res.IssuesUpserted)
	}

	// Broken binding errors out without affecting the healthy one.
	if _, err := brokenDriver.IngestBinding(ctx, brokenID); err == nil {
		t.Fatal("expected error from broken binding")
	}

	// Healthy binding's issue still in DB.
	got, err := repos.NewNormalizedIssueRepo(rls).GetByExternalID(ctx, "gh:test/healthy#1")
	if err != nil {
		t.Fatalf("GetByExternalID after broken failure: %v", err)
	}
	if got.Title != "ok" {
		t.Errorf("healthy issue lost; Title=%q", got.Title)
	}
}

func repoIDFromBinding(t *testing.T, rls *database.RLSPool, ctx context.Context, bindingID uuid.UUID) uuid.UUID {
	t.Helper()
	b, err := repos.NewTrackerBindingRepo(rls).Get(ctx, bindingID)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	return b.RepoID
}
