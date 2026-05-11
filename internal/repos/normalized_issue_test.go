package repos

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/revelara-ai/orion/internal/database"
)

// seedRepoBinding creates a ConnectedRepo + TrackerBinding so the
// normalized_issue FK constraints have something to point at.
func seedRepoBinding(t *testing.T, rls *database.RLSPool, ctx context.Context) (uuid.UUID, uuid.UUID) {
	t.Helper()
	repo, err := NewConnectedRepoRepo(rls).Create(ctx, ConnectedRepo{
		Provider: "github", AppInstallID: "1", RepoFullName: "test/repo",
	})
	if err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	binding, err := NewTrackerBindingRepo(rls).Create(ctx, TrackerBinding{
		RepoID: repo.ID, Kind: TrackerGitHubIssues, CredentialsRef: "vault://x",
	})
	if err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	return repo.ID, binding.ID
}

func TestNormalizedIssue_UpsertNew(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)
	repoID, bindingID := seedRepoBinding(t, rls, ctx)
	repo := NewNormalizedIssueRepo(rls)

	created, err := repo.Upsert(ctx, NormalizedIssue{
		RepoID:           repoID,
		TrackerBindingID: bindingID,
		ExternalID:       "gh:test/repo#42",
		ExternalURL:      "https://github.com/test/repo/issues/42",
		Title:            "first issue",
		Description:      "body",
		Labels:           []string{"bug"},
		State:            StateOpen,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Error("zero UUID")
	}
	if created.OrgID != orgID {
		t.Errorf("OrgID = %s", created.OrgID)
	}
	if created.ClaimStatus != ClaimUnclaimed {
		t.Errorf("ClaimStatus = %s, want unclaimed (default)", created.ClaimStatus)
	}
}

func TestNormalizedIssue_UpsertIdempotent(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)
	repoID, bindingID := seedRepoBinding(t, rls, ctx)
	repo := NewNormalizedIssueRepo(rls)

	mk := func(title string) NormalizedIssue {
		return NormalizedIssue{
			RepoID: repoID, TrackerBindingID: bindingID,
			ExternalID: "gh:test/repo#1", ExternalURL: "https://github.com/test/repo/issues/1",
			Title: title, State: StateOpen,
		}
	}
	a, _ := repo.Upsert(ctx, mk("v1"))
	b, _ := repo.Upsert(ctx, mk("v2"))
	if a.ID != b.ID {
		t.Errorf("Upsert idempotent on external_id should return same ID; %s vs %s", a.ID, b.ID)
	}
	if b.Title != "v2" {
		t.Errorf("title not updated; got %s", b.Title)
	}
}

func TestNormalizedIssue_UpsertPreservesDownstreamFields(t *testing.T) {
	// Verify that Upsert doesn't clobber eligibility / dedup_signature
	// / claim_status set by downstream slices (E2-7/E2-8) between
	// ingest ticks.
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)
	repoID, bindingID := seedRepoBinding(t, rls, ctx)
	repo := NewNormalizedIssueRepo(rls)

	created, err := repo.Upsert(ctx, NormalizedIssue{
		RepoID: repoID, TrackerBindingID: bindingID,
		ExternalID: "gh:test/repo#2", ExternalURL: "u", Title: "x", State: StateOpen,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Downstream slices stamp eligibility + dedup.
	if err := repo.UpdateEligibility(ctx, created.ID, EligEligible); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateDedupSignature(ctx, created.ID, "sha256:abc"); err != nil {
		t.Fatal(err)
	}
	// Next ingestion tick re-upserts.
	if _, err := repo.Upsert(ctx, NormalizedIssue{
		RepoID: repoID, TrackerBindingID: bindingID,
		ExternalID: "gh:test/repo#2", ExternalURL: "u-2", Title: "x-updated", State: StateOpen,
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Get(ctx, created.ID)
	if got.Title != "x-updated" {
		t.Errorf("title not updated: %s", got.Title)
	}
	if got.Eligibility == nil || *got.Eligibility != EligEligible {
		t.Errorf("eligibility clobbered: %v", got.Eligibility)
	}
	if got.DedupSignature == nil || *got.DedupSignature != "sha256:abc" {
		t.Errorf("dedup_signature clobbered: %v", got.DedupSignature)
	}
}

func TestNormalizedIssue_GetByExternalID(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)
	repoID, bindingID := seedRepoBinding(t, rls, ctx)
	repo := NewNormalizedIssueRepo(rls)
	_, _ = repo.Upsert(ctx, NormalizedIssue{
		RepoID: repoID, TrackerBindingID: bindingID,
		ExternalID: "lin:ENG-7", ExternalURL: "u", Title: "linear", State: StateOpen,
	})
	got, err := repo.GetByExternalID(ctx, "lin:ENG-7")
	if err != nil {
		t.Fatalf("GetByExternalID: %v", err)
	}
	if got.Title != "linear" {
		t.Errorf("title = %s", got.Title)
	}
}

func TestNormalizedIssue_ListByRepoWithEligibility(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)
	repoID, bindingID := seedRepoBinding(t, rls, ctx)
	repo := NewNormalizedIssueRepo(rls)

	// Seed 3 issues: 2 eligible, 1 ineligible_label.
	for i, e := range []Eligibility{EligEligible, EligEligible, EligIneligibleLabel} {
		c, _ := repo.Upsert(ctx, NormalizedIssue{
			RepoID: repoID, TrackerBindingID: bindingID,
			ExternalID:  fmt.Sprintf("gh:test/repo#%d", 100+i),
			ExternalURL: "u", Title: "x", State: StateOpen,
			LastSyncedAt: time.Now().UTC().Add(time.Duration(i) * time.Second),
		})
		if err := repo.UpdateEligibility(ctx, c.ID, e); err != nil {
			t.Fatal(err)
		}
	}
	wantElig := EligEligible
	got, err := repo.ListByRepo(ctx, repoID, ListByRepoOptions{Eligibility: &wantElig})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %d eligible, want 2", len(got))
	}
}

func TestNormalizedIssue_RLSIsolation(t *testing.T) {
	rls := newRLSPool(t)
	orgA := uuid.New()
	orgB := uuid.New()
	ctxA := database.WithRLSContext(context.Background(), "ua", orgA, nil)
	ctxB := database.WithRLSContext(context.Background(), "ub", orgB, nil)

	repoIDa, bindingIDa := seedRepoBinding(t, rls, ctxA)
	created, _ := NewNormalizedIssueRepo(rls).Upsert(ctxA, NormalizedIssue{
		RepoID: repoIDa, TrackerBindingID: bindingIDa,
		ExternalID: "gh:a/repo#1", ExternalURL: "u", Title: "x", State: StateOpen,
	})

	_, err := NewNormalizedIssueRepo(rls).Get(ctxB, created.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("orgB Get of orgA's issue: %v, want ErrNotFound", err)
	}
}

func TestNormalizedIssue_AutofileDedupGate(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)
	repoID, bindingID := seedRepoBinding(t, rls, ctx)
	repo := NewNormalizedIssueRepo(rls)

	// Orion-filed issue with dedup_signature 'sha256:gap-A'.
	c, _ := repo.Upsert(ctx, NormalizedIssue{
		RepoID: repoID, TrackerBindingID: bindingID,
		ExternalID: "gh:test/repo#filed-1", ExternalURL: "u", Title: "x", State: StateOpen,
		OrionFiled: true,
	})
	_ = repo.UpdateDedupSignature(ctx, c.ID, "sha256:gap-A")

	// E2-10's gate should detect the dup.
	exists, err := repo.ExistsOrionFiledByDedup(ctx, "sha256:gap-A")
	if err != nil {
		t.Fatalf("ExistsOrionFiledByDedup: %v", err)
	}
	if !exists {
		t.Error("expected dedup gate to detect orion-filed issue, got false")
	}

	// Different signature returns false.
	exists, _ = repo.ExistsOrionFiledByDedup(ctx, "sha256:gap-B")
	if exists {
		t.Error("unexpected dedup hit for new signature")
	}
}
