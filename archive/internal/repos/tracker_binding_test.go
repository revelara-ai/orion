package repos

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/revelara-ai/orion/internal/database"
)

func TestTrackerBinding_CreateGet(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)

	repoRepo := NewConnectedRepoRepo(rls)
	repo, err := repoRepo.Create(ctx, ConnectedRepo{
		Provider: "github", AppInstallID: "1", RepoFullName: "x/y",
	})
	if err != nil {
		t.Fatal(err)
	}

	bindingRepo := NewTrackerBindingRepo(rls)
	binding, err := bindingRepo.Create(ctx, TrackerBinding{
		RepoID:         repo.ID,
		Kind:           TrackerLinear,
		CredentialsRef: "vault://linear/test",
		Config:         map[string]any{"workspace": "test-workspace"},
		Enabled:        true,
		AutoFile:       false,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if binding.ID == uuid.Nil {
		t.Error("zero UUID")
	}

	got, err := bindingRepo.Get(ctx, binding.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Kind != TrackerLinear {
		t.Errorf("Kind = %s, want linear", got.Kind)
	}
	if got.Config["workspace"] != "test-workspace" {
		t.Errorf("Config = %v", got.Config)
	}
}

func TestTrackerBinding_ListByRepo(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)

	repoRepo := NewConnectedRepoRepo(rls)
	repo, _ := repoRepo.Create(ctx, ConnectedRepo{
		Provider: "github", AppInstallID: "1", RepoFullName: "x/y",
	})

	bindingRepo := NewTrackerBindingRepo(rls)
	for _, k := range []TrackerKind{TrackerGitHubIssues, TrackerLinear} {
		if _, err := bindingRepo.Create(ctx, TrackerBinding{
			RepoID: repo.ID, Kind: k, CredentialsRef: "vault://" + string(k),
		}); err != nil {
			t.Fatal(err)
		}
	}
	bindings, err := bindingRepo.ListByRepo(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 {
		t.Errorf("got %d bindings, want 2", len(bindings))
	}
}

func TestTrackerBinding_RLSIsolation(t *testing.T) {
	rls := newRLSPool(t)
	orgA := uuid.New()
	orgB := uuid.New()
	ctxA := database.WithRLSContext(context.Background(), "ua", orgA, nil)
	ctxB := database.WithRLSContext(context.Background(), "ub", orgB, nil)

	repoA, err := NewConnectedRepoRepo(rls).Create(ctxA, ConnectedRepo{
		Provider: "github", AppInstallID: "1", RepoFullName: "a/r",
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := NewTrackerBindingRepo(rls).Create(ctxA, TrackerBinding{
		RepoID: repoA.ID, Kind: TrackerGitHubIssues, CredentialsRef: "x",
	})
	if err != nil {
		t.Fatal(err)
	}

	// orgB can't see orgA's binding.
	_, err = NewTrackerBindingRepo(rls).Get(ctxB, binding.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("orgB Get orgA's binding: err = %v, want ErrNotFound", err)
	}
}
