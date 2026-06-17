package repos

// RunRepo contract tests (SPEC §7.1).

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/database"
)

// seedRunOrg seeds an organization + connected_repo and returns
// (orgID, repoID, rls). The rls pool is bound to the seeded ctx.
func seedRunOrg(t *testing.T) (uuid.UUID, uuid.UUID, *database.RLSPool, context.Context) {
	t.Helper()
	pool := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), uuid.NewString(), orgID, nil)
	repoRepo := NewConnectedRepoRepo(pool)
	cr, err := repoRepo.Create(ctx, ConnectedRepo{
		Provider:      "github",
		AppInstallID:  "install-1",
		RepoFullName:  "acme/svc",
		DefaultBranch: "main",
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("seed ConnectedRepo: %v", err)
	}
	return orgID, cr.ID, pool, ctx
}

func TestRunRepo_CreateAndGet(t *testing.T) {
	orgID, repoID, pool, ctx := seedRunOrg(t)
	runs := NewRunRepo(pool)

	r, err := runs.Create(ctx, Run{
		RepoID: repoID,
		Status: RunCreated,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r.ID == uuid.Nil {
		t.Error("ID not assigned")
	}
	if r.OrgID != orgID {
		t.Errorf("OrgID = %v; want %v (RLS)", r.OrgID, orgID)
	}
	if r.Status != RunCreated {
		t.Errorf("Status = %q; want %q", r.Status, RunCreated)
	}

	got, err := runs.Get(ctx, r.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != r.ID {
		t.Errorf("Get returned different row: %v vs %v", got.ID, r.ID)
	}
}

func TestRunRepo_UpdateStatus_TransitionsAllowed(t *testing.T) {
	_, repoID, pool, ctx := seedRunOrg(t)
	runs := NewRunRepo(pool)
	r, err := runs.Create(ctx, Run{RepoID: repoID, Status: RunCreated})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := runs.UpdateStatus(ctx, r.ID, RunScanning); err != nil {
		t.Fatalf("UpdateStatus scanning: %v", err)
	}
	got, _ := runs.Get(ctx, r.ID)
	if got.Status != RunScanning {
		t.Errorf("Status after update = %q; want %q", got.Status, RunScanning)
	}
}

func TestRunRepo_UpdateStatus_RejectsBogusValue(t *testing.T) {
	_, repoID, pool, ctx := seedRunOrg(t)
	runs := NewRunRepo(pool)
	r, err := runs.Create(ctx, Run{RepoID: repoID, Status: RunCreated})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := runs.UpdateStatus(ctx, r.ID, RunState("not_a_state")); err == nil {
		t.Fatal("expected CHECK constraint failure for unknown state")
	}
}

func TestRunRepo_ListInState_FiltersTerminalStates(t *testing.T) {
	_, repoID, pool, ctx := seedRunOrg(t)
	runs := NewRunRepo(pool)

	for _, st := range []RunState{
		RunCreated, RunScanning, RunBacklogActive,
		RunCompleted, RunFailed,
	} {
		if _, err := runs.Create(ctx, Run{RepoID: repoID, Status: st}); err != nil {
			t.Fatalf("Create(%s): %v", st, err)
		}
	}

	// Non-terminal: created, scanning, backlog_active, draining,
	// inventorying, paused. Completed/failed/cancelled/config_invalid
	// are terminal. ListInState filters strictly by the argument set.
	list, err := runs.ListInState(ctx, []RunState{
		RunCreated, RunScanning, RunBacklogActive,
	})
	if err != nil {
		t.Fatalf("ListInState: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("ListInState returned %d rows; want 3 (created+scanning+backlog_active)", len(list))
	}
	for _, r := range list {
		if r.Status == RunCompleted || r.Status == RunFailed {
			t.Errorf("ListInState returned terminal-state run: %s", r.Status)
		}
	}
}

func TestRunRepo_RLS_IsolatesAcrossOrgs(t *testing.T) {
	_, _, pool, _ := seedRunOrg(t)

	orgA := uuid.New()
	orgB := uuid.New()
	ctxA := database.WithRLSContext(context.Background(), uuid.NewString(), orgA, nil)
	ctxB := database.WithRLSContext(context.Background(), uuid.NewString(), orgB, nil)

	repoRepo := NewConnectedRepoRepo(pool)
	crA, err := repoRepo.Create(ctxA, ConnectedRepo{
		Provider: "github", AppInstallID: "iA", RepoFullName: "a/x", DefaultBranch: "main", Enabled: true,
	})
	if err != nil {
		t.Fatalf("seed A: %v", err)
	}
	if _, err := repoRepo.Create(ctxB, ConnectedRepo{
		Provider: "github", AppInstallID: "iB", RepoFullName: "b/x", DefaultBranch: "main", Enabled: true,
	}); err != nil {
		t.Fatalf("seed B: %v", err)
	}

	runs := NewRunRepo(pool)
	rA, err := runs.Create(ctxA, Run{RepoID: crA.ID, Status: RunCreated})
	if err != nil {
		t.Fatalf("Create A: %v", err)
	}

	// Org B must not see Org A's run.
	if _, err := runs.Get(ctxB, rA.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("org B Get(rA) = %v; want ErrNotFound (RLS)", err)
	}
	listB, _ := runs.ListInState(ctxB, []RunState{RunCreated})
	if len(listB) != 0 {
		t.Errorf("org B ListInState returned %d rows; want 0 (RLS)", len(listB))
	}
}

func TestRunRepo_CreateRejectsUnknownStatus(t *testing.T) {
	_, repoID, pool, ctx := seedRunOrg(t)
	runs := NewRunRepo(pool)
	_, err := runs.Create(ctx, Run{RepoID: repoID, Status: RunState("garbage")})
	if err == nil {
		t.Fatal("expected CHECK constraint failure for unknown status on create")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "constraint") && !strings.Contains(strings.ToLower(err.Error()), "check") {
		t.Errorf("expected check-constraint error, got %v", err)
	}
}
