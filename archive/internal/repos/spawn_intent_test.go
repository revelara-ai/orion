package repos

// SpawnIntentRepo contract tests (SPEC §7.4 #6).

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestSpawnIntentRepo_Record(t *testing.T) {
	_, runID, pool, ctx := seedClaimEnv(t)
	claims := NewClaimRepo(pool)
	c, _ := claims.Claim(ctx, ClaimInput{
		RunID:           runID,
		IssueExternalID: "gh#intent",
		FencingToken:    1,
		InitialState:    ClaimClaimedState,
	})
	intents := NewSpawnIntentRepo(pool)
	si, err := intents.Record(ctx, SpawnIntentInput{
		ClaimID:        c.ID,
		WorkspaceKey:   "ws-intent",
		PodNamespace:   "orion-tenant-a",
		PodNamePlanned: "orion-worker-1",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if si.WorkspaceKey != "ws-intent" {
		t.Errorf("WorkspaceKey = %q; want %q", si.WorkspaceKey, "ws-intent")
	}
	if si.PodNamespace != "orion-tenant-a" || si.PodNamePlanned != "orion-worker-1" {
		t.Errorf("namespace/podname mismatch: %+v", si)
	}
}

func TestSpawnIntentRepo_DuplicateWorkspaceKey(t *testing.T) {
	_, runID, pool, ctx := seedClaimEnv(t)
	claims := NewClaimRepo(pool)
	c1, _ := claims.Claim(ctx, ClaimInput{
		RunID:           runID,
		IssueExternalID: "gh#i1",
		FencingToken:    1,
		InitialState:    ClaimClaimedState,
	})
	c2, _ := claims.Claim(ctx, ClaimInput{
		RunID:           runID,
		IssueExternalID: "gh#i2",
		FencingToken:    1,
		InitialState:    ClaimClaimedState,
	})
	intents := NewSpawnIntentRepo(pool)
	if _, err := intents.Record(ctx, SpawnIntentInput{
		ClaimID:        c1.ID,
		WorkspaceKey:   "ws-dup",
		PodNamespace:   "ns",
		PodNamePlanned: "p1",
	}); err != nil {
		t.Fatalf("first Record: %v", err)
	}
	_, err := intents.Record(ctx, SpawnIntentInput{
		ClaimID:        c2.ID,
		WorkspaceKey:   "ws-dup",
		PodNamespace:   "ns",
		PodNamePlanned: "p2",
	})
	if !errors.Is(err, ErrWorkspaceKeyTaken) {
		t.Errorf("err = %v; want ErrWorkspaceKeyTaken", err)
	}
}

func TestSpawnIntentRepo_GetByWorkspaceKey(t *testing.T) {
	_, runID, pool, ctx := seedClaimEnv(t)
	claims := NewClaimRepo(pool)
	c, _ := claims.Claim(ctx, ClaimInput{
		RunID:           runID,
		IssueExternalID: "gh#getby",
		FencingToken:    1,
		InitialState:    ClaimClaimedState,
	})
	intents := NewSpawnIntentRepo(pool)
	si, _ := intents.Record(ctx, SpawnIntentInput{
		ClaimID:        c.ID,
		WorkspaceKey:   "ws-getby",
		PodNamespace:   "ns",
		PodNamePlanned: "p",
	})

	got, err := intents.GetByWorkspaceKey(ctx, "ws-getby")
	if err != nil {
		t.Fatalf("GetByWorkspaceKey: %v", err)
	}
	if got.ID != si.ID {
		t.Errorf("ID mismatch: %v vs %v", got.ID, si.ID)
	}

	if _, err := intents.GetByWorkspaceKey(ctx, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing key: err = %v; want ErrNotFound", err)
	}
}
