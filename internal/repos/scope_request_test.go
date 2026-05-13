package repos

// ScopeRequestRepo contract tests (SPEC §11.3 audit ledger).

import (
	"testing"

	"github.com/google/uuid"
)

func TestScopeRequestRepo_Record_Accepted(t *testing.T) {
	_, runID, pool, ctx := seedClaimEnv(t)
	claims := NewClaimRepo(pool)
	c, _ := claims.Claim(ctx, ClaimInput{
		RunID:           runID,
		IssueExternalID: "gh#scope",
		FencingToken:    1,
		InitialState:    ClaimClaimedState,
	})
	repo := NewScopeRequestRepo(pool)
	cid := c.ID
	sr, err := repo.Record(ctx, ScopeRequestInput{
		RunID:          runID,
		ClaimID:        &cid,
		ToolName:       "apply_patch",
		RequestedScope: map[string]any{"path": "src/main.go"},
		GrantedScope:   map[string]any{"path": "src/main.go", "bytes": 42},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if sr.ToolName != "apply_patch" {
		t.Errorf("ToolName = %q; want %q", sr.ToolName, "apply_patch")
	}
	if sr.RejectionReason != nil {
		t.Errorf("RejectionReason = %v; want nil for accepted dispatch", sr.RejectionReason)
	}
	if sr.RequestedScope["path"] != "src/main.go" {
		t.Errorf("RequestedScope[path] = %v; want src/main.go", sr.RequestedScope["path"])
	}
}

func TestScopeRequestRepo_Record_Rejected(t *testing.T) {
	_, runID, pool, ctx := seedClaimEnv(t)
	repo := NewScopeRequestRepo(pool)
	reason := "path escapes workspace root"
	sr, err := repo.Record(ctx, ScopeRequestInput{
		RunID:           runID,
		ToolName:        "apply_patch",
		RequestedScope:  map[string]any{"path": "../etc/passwd"},
		RejectionReason: &reason,
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if sr.RejectionReason == nil || *sr.RejectionReason != reason {
		t.Errorf("RejectionReason = %v; want %q", sr.RejectionReason, reason)
	}
}

func TestScopeRequestRepo_ListByWorkerSession(t *testing.T) {
	_, runID, pool, ctx := seedClaimEnv(t)
	claims := NewClaimRepo(pool)
	c, _ := claims.Claim(ctx, ClaimInput{
		RunID: runID, IssueExternalID: "gh#list", FencingToken: 1, InitialState: ClaimClaimedState,
	})
	sessions := NewWorkerSessionRepo(pool)
	ws, _ := sessions.Create(ctx, WorkerSessionInput{
		RunID: runID, ClaimID: c.ID, WorkspaceKey: "ws-list",
	})
	repo := NewScopeRequestRepo(pool)
	wsID := ws.ID
	for i := 0; i < 3; i++ {
		if _, err := repo.Record(ctx, ScopeRequestInput{
			RunID:           runID,
			WorkerSessionID: &wsID,
			ToolName:        "read_file",
			RequestedScope:  map[string]any{"path": "x"},
		}); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}
	got, err := repo.ListByWorkerSession(ctx, ws.ID)
	if err != nil {
		t.Fatalf("ListByWorkerSession: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("len(got) = %d; want 3", len(got))
	}

	// Other sessions return empty.
	other := uuid.New()
	if got2, _ := repo.ListByWorkerSession(ctx, other); len(got2) != 0 {
		t.Errorf("other session list len = %d; want 0", len(got2))
	}
}
