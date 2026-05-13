package repos

// WorkerSessionRepo contract tests (SPEC §7.3, §11.1 idempotency).

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// seedWorkerEnv builds (orgID, runID, claimID, rls, ctx).
func seedWorkerEnv(t *testing.T) (uuid.UUID, uuid.UUID, uuid.UUID, context.Context) {
	t.Helper()
	orgID, runID, pool, ctx := seedClaimEnv(t)
	claims := NewClaimRepo(pool)
	c, err := claims.Claim(ctx, ClaimInput{
		RunID:           runID,
		IssueExternalID: "gh#worker-seed",
		FencingToken:    1,
		InitialState:    ClaimClaimedState,
	})
	if err != nil {
		t.Fatalf("seed Claim: %v", err)
	}
	return orgID, runID, c.ID, ctx
}

func TestWorkerSessionRepo_Create(t *testing.T) {
	orgID, runID, claimID, ctx := seedWorkerEnv(t)
	_, _, pool, _ := seedClaimEnv(t)
	_ = pool

	// Re-derive the pool from ctx — seedClaimEnv keeps the pool
	// bound, but seedWorkerEnv doesn't return it. Easiest: re-seed
	// for clarity.
	_, runID2, pool2, ctx2 := seedClaimEnv(t)
	claims2 := NewClaimRepo(pool2)
	c, _ := claims2.Claim(ctx2, ClaimInput{
		RunID:           runID2,
		IssueExternalID: "gh#worker-create",
		FencingToken:    1,
		InitialState:    ClaimClaimedState,
	})
	sessions := NewWorkerSessionRepo(pool2)

	ws, err := sessions.Create(ctx2, WorkerSessionInput{
		RunID:        runID2,
		ClaimID:      c.ID,
		WorkspaceKey: "ws-create",
		InitialPhase: WorkerPhasePreparingSandbox,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ws.WorkspaceKey != "ws-create" {
		t.Errorf("WorkspaceKey = %q; want %q", ws.WorkspaceKey, "ws-create")
	}
	if ws.Phase != WorkerPhasePreparingSandbox {
		t.Errorf("Phase = %q; want %q", ws.Phase, WorkerPhasePreparingSandbox)
	}
	_ = orgID
	_ = runID
	_ = claimID
	_ = ctx
}

func TestWorkerSessionRepo_DuplicateWorkspaceKeyFails(t *testing.T) {
	_, runID, pool, ctx := seedClaimEnv(t)
	claims := NewClaimRepo(pool)
	c, _ := claims.Claim(ctx, ClaimInput{
		RunID:           runID,
		IssueExternalID: "gh#dup",
		FencingToken:    1,
		InitialState:    ClaimClaimedState,
	})
	sessions := NewWorkerSessionRepo(pool)
	if _, err := sessions.Create(ctx, WorkerSessionInput{
		RunID: runID, ClaimID: c.ID, WorkspaceKey: "ws-dup",
	}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	// Second claim for a different issue but same workspace_key
	// (operator error). UNIQUE must catch it.
	c2, _ := claims.Claim(ctx, ClaimInput{
		RunID:           runID,
		IssueExternalID: "gh#dup-2",
		FencingToken:    1,
		InitialState:    ClaimClaimedState,
	})
	_, err := sessions.Create(ctx, WorkerSessionInput{
		RunID: runID, ClaimID: c2.ID, WorkspaceKey: "ws-dup",
	})
	if err == nil {
		t.Fatal("expected duplicate workspace_key to fail")
	}
	if !errors.Is(err, ErrWorkspaceKeyTaken) {
		t.Errorf("err = %v; want ErrWorkspaceKeyTaken", err)
	}
}

func TestWorkerSessionRepo_ConcurrentSameWorkspaceKey_OneWins(t *testing.T) {
	_, runID, pool, ctx := seedClaimEnv(t)
	claims := NewClaimRepo(pool)
	sessions := NewWorkerSessionRepo(pool)

	const N = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var winners, contended int
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			c, err := claims.Claim(ctx, ClaimInput{
				RunID:           runID,
				IssueExternalID: uuid.NewString(),
				FencingToken:    int64(idx + 1),
				InitialState:    ClaimClaimedState,
			})
			if err != nil {
				t.Errorf("Claim: %v", err)
				return
			}
			_, err = sessions.Create(ctx, WorkerSessionInput{
				RunID:        runID,
				ClaimID:      c.ID,
				WorkspaceKey: "ws-shared",
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				winners++
			} else if errors.Is(err, ErrWorkspaceKeyTaken) {
				contended++
			} else {
				t.Errorf("unexpected err: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if winners != 1 {
		t.Errorf("winners = %d; want exactly 1 (workspace_key UNIQUE)", winners)
	}
	if contended != N-1 {
		t.Errorf("contended = %d; want %d", contended, N-1)
	}
}

func TestWorkerSessionRepo_UpdatePhase(t *testing.T) {
	_, runID, pool, ctx := seedClaimEnv(t)
	claims := NewClaimRepo(pool)
	c, _ := claims.Claim(ctx, ClaimInput{
		RunID:           runID,
		IssueExternalID: "gh#phase",
		FencingToken:    1,
		InitialState:    ClaimClaimedState,
	})
	sessions := NewWorkerSessionRepo(pool)
	ws, _ := sessions.Create(ctx, WorkerSessionInput{
		RunID: runID, ClaimID: c.ID, WorkspaceKey: "ws-phase",
	})
	if err := sessions.UpdatePhase(ctx, ws.ID, WorkerPhaseSynthesizingPatches); err != nil {
		t.Fatalf("UpdatePhase: %v", err)
	}
	got, _ := sessions.Get(ctx, ws.ID)
	if got.Phase != WorkerPhaseSynthesizingPatches {
		t.Errorf("Phase after update = %q; want %q", got.Phase, WorkerPhaseSynthesizingPatches)
	}
	if got.FinishedAt != nil {
		t.Error("FinishedAt should be nil for non-terminal phase")
	}

	if err := sessions.UpdatePhase(ctx, ws.ID, WorkerPhaseSucceeded); err != nil {
		t.Fatalf("UpdatePhase succeeded: %v", err)
	}
	got, _ = sessions.Get(ctx, ws.ID)
	if got.FinishedAt == nil {
		t.Error("FinishedAt should be set after terminal phase (succeeded)")
	}
}

func TestWorkerSessionRepo_UpdatePhase_RejectsBogusValue(t *testing.T) {
	_, runID, pool, ctx := seedClaimEnv(t)
	claims := NewClaimRepo(pool)
	c, _ := claims.Claim(ctx, ClaimInput{
		RunID:           runID,
		IssueExternalID: "gh#bogus",
		FencingToken:    1,
		InitialState:    ClaimClaimedState,
	})
	sessions := NewWorkerSessionRepo(pool)
	ws, _ := sessions.Create(ctx, WorkerSessionInput{
		RunID: runID, ClaimID: c.ID, WorkspaceKey: "ws-bogus",
	})
	if err := sessions.UpdatePhase(ctx, ws.ID, WorkerSessionPhase("not_a_phase")); err == nil {
		t.Fatal("expected CHECK constraint failure for unknown phase")
	}
}
