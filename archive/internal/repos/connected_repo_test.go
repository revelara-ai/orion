package repos

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/revelara-ai/orion/internal/database"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// newRLSPool spins a pg container, runs migrations, returns an
// RLSPool ready for tests. Skips the test if testcontainers can't
// stand a container up (e.g. CI without Docker).
func newRLSPool(t *testing.T) *database.RLSPool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping testcontainer-backed test in -short mode")
	}
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

func TestConnectedRepo_CreateGet(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "test-user", orgID, nil)
	repo := NewConnectedRepoRepo(rls)

	created, err := repo.Create(ctx, ConnectedRepo{
		Provider:     "github",
		AppInstallID: "12345",
		RepoFullName: "revelara-ai/orion",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Error("Create returned zero UUID")
	}
	if created.OrgID != orgID {
		t.Errorf("OrgID = %s, want %s", created.OrgID, orgID)
	}
	if created.TrustMode != TrustShadow {
		t.Errorf("TrustMode = %s, want shadow (default)", created.TrustMode)
	}

	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RepoFullName != "revelara-ai/orion" {
		t.Errorf("Get returned %s, want revelara-ai/orion", got.RepoFullName)
	}
}

func TestConnectedRepo_RLSIsolation(t *testing.T) {
	rls := newRLSPool(t)
	orgA := uuid.New()
	orgB := uuid.New()
	repo := NewConnectedRepoRepo(rls)

	// orgA creates a repo.
	ctxA := database.WithRLSContext(context.Background(), "user-a", orgA, nil)
	created, err := repo.Create(ctxA, ConnectedRepo{
		Provider: "github", AppInstallID: "1", RepoFullName: "a/repo",
	})
	if err != nil {
		t.Fatalf("orgA Create: %v", err)
	}

	// orgB attempts to Get orgA's repo by id — RLS hides it.
	ctxB := database.WithRLSContext(context.Background(), "user-b", orgB, nil)
	_, err = repo.Get(ctxB, created.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("orgB Get of orgA's repo: err = %v, want ErrNotFound", err)
	}

	// orgB's ListByOrg returns empty.
	rows, err := repo.ListByOrg(ctxB)
	if err != nil {
		t.Fatalf("orgB ListByOrg: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("orgB sees %d repos, want 0 (RLS leak!)", len(rows))
	}
}

func TestConnectedRepo_UpdateTrustMode(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "user", orgID, nil)
	repo := NewConnectedRepoRepo(rls)

	created, err := repo.Create(ctx, ConnectedRepo{
		Provider: "github", AppInstallID: "1", RepoFullName: "x/y",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateTrustMode(ctx, created.ID, TrustFull); err != nil {
		t.Fatalf("UpdateTrustMode: %v", err)
	}
	got, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TrustMode != TrustFull {
		t.Errorf("trust = %s, want full", got.TrustMode)
	}
}

func TestConnectedRepo_DeleteCascadesToBindings(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "user", orgID, nil)
	repoRepo := NewConnectedRepoRepo(rls)
	bindingRepo := NewTrackerBindingRepo(rls)

	repo, err := repoRepo.Create(ctx, ConnectedRepo{
		Provider: "github", AppInstallID: "1", RepoFullName: "x/y",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bindingRepo.Create(ctx, TrackerBinding{
		RepoID: repo.ID, Kind: TrackerGitHubIssues, CredentialsRef: "vault://x",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repoRepo.Delete(ctx, repo.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	bindings, err := bindingRepo.ListByRepo(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 0 {
		t.Errorf("expected cascaded delete to remove bindings, got %d", len(bindings))
	}
}
