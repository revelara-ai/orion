package repos

// ClaimRepo contract tests (SPEC §7.2, §7.4 #1 idempotency).

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/database"
)

// seedClaimEnv builds (orgID, runID, rls, ctx) for ClaimRepo tests.
func seedClaimEnv(t *testing.T) (uuid.UUID, uuid.UUID, *database.RLSPool, context.Context) {
	t.Helper()
	orgID, repoID, pool, ctx := seedRunOrg(t)
	runs := NewRunRepo(pool)
	r, err := runs.Create(ctx, Run{RepoID: repoID, Status: RunBacklogActive})
	if err != nil {
		t.Fatalf("seed Run: %v", err)
	}
	return orgID, r.ID, pool, ctx
}

func TestClaimRepo_FirstClaimSucceeds(t *testing.T) {
	_, runID, pool, ctx := seedClaimEnv(t)
	claims := NewClaimRepo(pool)

	c, err := claims.Claim(ctx, ClaimInput{
		RunID:           runID,
		IssueExternalID: "gh#1",
		FencingToken:    1,
		InitialState:    ClaimClaimedState,
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if c == nil {
		t.Fatal("Claim returned nil row")
	}
	if c.State != ClaimClaimedState {
		t.Errorf("State = %q; want %q", c.State, ClaimClaimedState)
	}
	if c.FencingToken == nil || *c.FencingToken != 1 {
		t.Errorf("FencingToken = %v; want 1", c.FencingToken)
	}
}

func TestClaimRepo_SecondClaimSameExternalIDFails(t *testing.T) {
	_, runID, pool, ctx := seedClaimEnv(t)
	claims := NewClaimRepo(pool)

	if _, err := claims.Claim(ctx, ClaimInput{
		RunID:           runID,
		IssueExternalID: "gh#1",
		FencingToken:    1,
		InitialState:    ClaimClaimedState,
	}); err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	_, err := claims.Claim(ctx, ClaimInput{
		RunID:           runID,
		IssueExternalID: "gh#1",
		FencingToken:    2,
		InitialState:    ClaimClaimedState,
	})
	if err == nil {
		t.Fatal("second Claim with same external_id should have failed")
	}
	if !errors.Is(err, ErrAlreadyClaimed) {
		t.Errorf("err = %v; want ErrAlreadyClaimed (UNIQUE constraint per SPEC §7.4 #1)", err)
	}
}

func TestClaimRepo_ConcurrentClaimOnlyOneWins(t *testing.T) {
	_, runID, pool, ctx := seedClaimEnv(t)
	claims := NewClaimRepo(pool)

	const N = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var winners int
	var alreadyClaimed int
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := claims.Claim(ctx, ClaimInput{
				RunID:           runID,
				IssueExternalID: "gh#race",
				FencingToken:    1,
				InitialState:    ClaimClaimedState,
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				winners++
			} else if errors.Is(err, ErrAlreadyClaimed) {
				alreadyClaimed++
			} else {
				t.Errorf("unexpected err: %v", err)
			}
		}()
	}
	wg.Wait()

	if winners != 1 {
		t.Errorf("winners = %d; want exactly 1 (UNIQUE constraint)", winners)
	}
	if alreadyClaimed != N-1 {
		t.Errorf("alreadyClaimed = %d; want %d", alreadyClaimed, N-1)
	}
}

func TestClaimRepo_UpdateState(t *testing.T) {
	_, runID, pool, ctx := seedClaimEnv(t)
	claims := NewClaimRepo(pool)

	c, err := claims.Claim(ctx, ClaimInput{
		RunID:           runID,
		IssueExternalID: "gh#1",
		FencingToken:    1,
		InitialState:    ClaimClaimedState,
	})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if err := claims.UpdateState(ctx, c.ID, ClaimDispatchedState); err != nil {
		t.Fatalf("UpdateState dispatched: %v", err)
	}
	got, err := claims.Get(ctx, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != ClaimDispatchedState {
		t.Errorf("State after update = %q; want %q", got.State, ClaimDispatchedState)
	}
}

func TestClaimRepo_UpdateState_RejectsBogusValue(t *testing.T) {
	_, runID, pool, ctx := seedClaimEnv(t)
	claims := NewClaimRepo(pool)
	c, _ := claims.Claim(ctx, ClaimInput{
		RunID:           runID,
		IssueExternalID: "gh#1",
		FencingToken:    1,
		InitialState:    ClaimClaimedState,
	})
	if err := claims.UpdateState(ctx, c.ID, ClaimState("not_a_state")); err == nil {
		t.Fatal("expected CHECK constraint failure for unknown state")
	}
}

func TestClaimRepo_RLS_IsolatesAcrossOrgs(t *testing.T) {
	// Org A claims shared#1 against its own run. Org B (different RLS
	// scope, same DB) must not see Org A's row AND must be able to
	// claim the same external_id under its own scope since UNIQUE is
	// (org_id, issue_external_id).
	_, runIDA, pool, ctxA := seedClaimEnv(t)
	claims := NewClaimRepo(pool)
	cA, err := claims.Claim(ctxA, ClaimInput{
		RunID:           runIDA,
		IssueExternalID: "shared#1",
		FencingToken:    1,
		InitialState:    ClaimClaimedState,
	})
	if err != nil {
		t.Fatalf("ctxA Claim: %v", err)
	}

	orgB := uuid.New()
	ctxB := database.WithRLSContext(context.Background(), uuid.NewString(), orgB, nil)
	repoRepo := NewConnectedRepoRepo(pool)
	crB, err := repoRepo.Create(ctxB, ConnectedRepo{
		Provider: "github", AppInstallID: "iB", RepoFullName: "b/x", DefaultBranch: "main", Enabled: true,
	})
	if err != nil {
		t.Fatalf("seed B repo: %v", err)
	}
	rB, err := NewRunRepo(pool).Create(ctxB, Run{RepoID: crB.ID, Status: RunBacklogActive})
	if err != nil {
		t.Fatalf("seed B run: %v", err)
	}

	// Org B cannot see Org A's row.
	if _, err := claims.Get(ctxB, cA.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("ctxB Get(cA) = %v; want ErrNotFound (RLS)", err)
	}

	// Org B can claim the same external_id under its own scope.
	if _, err := claims.Claim(ctxB, ClaimInput{
		RunID:           rB.ID,
		IssueExternalID: "shared#1",
		FencingToken:    1,
		InitialState:    ClaimClaimedState,
	}); err != nil {
		t.Errorf("ctxB Claim(shared#1) failed; UNIQUE is per-org, not global: %v", err)
	}
}
