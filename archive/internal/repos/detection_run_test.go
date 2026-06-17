package repos

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/database"
)

// seedRunPrereqs creates the ConnectedRepo + TrackerBinding so the
// detection_runs.binding_id FK has something to point at within the
// caller's RLS scope.
func seedRunPrereqs(t *testing.T, rls *database.RLSPool, ctx context.Context) uuid.UUID {
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
	return binding.ID
}

func TestDetectionRun_CreateAndGet(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)
	bindingID := seedRunPrereqs(t, rls, ctx)
	repo := NewDetectionRunRepo(rls)

	created, err := repo.Create(ctx, DetectionRun{
		BindingID:           bindingID,
		Mode:                DetectionModeFull,
		Phase:               DetectionPhaseCompleted,
		FindingsTotal:       3,
		FindingsNew:         3,
		OrionFiledProcessed: 3,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Error("Create returned zero UUID")
	}
	if created.OrgID != orgID {
		t.Errorf("OrgID = %s, want %s (from RLS context)", created.OrgID, orgID)
	}
	if created.StartedAt.IsZero() {
		t.Error("StartedAt should be set by DB default")
	}

	got, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("GetByID returned ID %s, want %s", got.ID, created.ID)
	}
	if got.Mode != DetectionModeFull {
		t.Errorf("Mode = %s, want full", got.Mode)
	}
	if got.Phase != DetectionPhaseCompleted {
		t.Errorf("Phase = %s, want completed", got.Phase)
	}
	if got.FindingsTotal != 3 {
		t.Errorf("FindingsTotal = %d, want 3", got.FindingsTotal)
	}
}

func TestDetectionRun_GetByID_NotFound(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)
	_ = seedRunPrereqs(t, rls, ctx)
	repo := NewDetectionRunRepo(rls)

	_, err := repo.GetByID(ctx, uuid.New())
	if !errors.Is(err, ErrDetectionRunNotFound) {
		t.Errorf("expected ErrDetectionRunNotFound, got %v", err)
	}
}

func TestDetectionRun_RLSIsolation(t *testing.T) {
	rls := newRLSPool(t)
	orgA := uuid.New()
	orgB := uuid.New()
	ctxA := database.WithRLSContext(context.Background(), "ua", orgA, nil)
	ctxB := database.WithRLSContext(context.Background(), "ub", orgB, nil)

	bindingA := seedRunPrereqs(t, rls, ctxA)
	bindingB := seedRunPrereqs(t, rls, ctxB)
	repo := NewDetectionRunRepo(rls)

	createdA, err := repo.Create(ctxA, DetectionRun{
		BindingID: bindingA, Mode: DetectionModeFull, Phase: DetectionPhaseCompleted,
	})
	if err != nil {
		t.Fatalf("Create A: %v", err)
	}
	_, err = repo.Create(ctxB, DetectionRun{
		BindingID: bindingB, Mode: DetectionModeFull, Phase: DetectionPhaseCompleted,
	})
	if err != nil {
		t.Fatalf("Create B: %v", err)
	}

	// Org B must NOT see Org A's run.
	_, err = repo.GetByID(ctxB, createdA.ID)
	if !errors.Is(err, ErrDetectionRunNotFound) {
		t.Errorf("RLS leak: orgB read orgA's run; err=%v", err)
	}

	// Org A sees only its own listing.
	listed, err := repo.ListByBinding(ctxA, bindingA, 0)
	if err != nil {
		t.Fatalf("ListByBinding A: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != createdA.ID {
		t.Errorf("ListByBinding A: got %d rows, want 1 (createdA)", len(listed))
	}
}

func TestDetectionRun_CountByBinding(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)
	bindingID := seedRunPrereqs(t, rls, ctx)
	repo := NewDetectionRunRepo(rls)

	n0, err := repo.CountByBinding(ctx, bindingID)
	if err != nil || n0 != 0 {
		t.Fatalf("CountByBinding initial: got %d err=%v, want 0", n0, err)
	}

	for i := 0; i < 3; i++ {
		if _, err := repo.Create(ctx, DetectionRun{
			BindingID: bindingID, Mode: DetectionModeFull, Phase: DetectionPhaseCompleted,
		}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	n3, err := repo.CountByBinding(ctx, bindingID)
	if err != nil || n3 != 3 {
		t.Fatalf("CountByBinding after 3 creates: got %d err=%v, want 3", n3, err)
	}
}

func TestDetectionFinding_CreateBatchAndListByRun(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)
	bindingID := seedRunPrereqs(t, rls, ctx)
	runRepo := NewDetectionRunRepo(rls)
	findRepo := NewDetectionFindingRepo(rls)

	run, err := runRepo.Create(ctx, DetectionRun{
		BindingID: bindingID, Mode: DetectionModeFull, Phase: DetectionPhaseCompleted, FindingsTotal: 2,
	})
	if err != nil {
		t.Fatalf("Create run: %v", err)
	}

	sig1 := "sig-abc"
	batch := []DetectionFinding{
		{
			RunID:          run.ID,
			Slug:           "missing-timeout",
			Title:          "http.Client without Timeout",
			Category:       "fault_tolerance",
			Confidence:     "high",
			Severity:       "high",
			ControlCodes:   []string{"RC-018"},
			FilePath:       "client.go",
			LineNo:         11,
			Fingerprint:    "loc-001",
			DedupSignature: &sig1,
		},
		{
			RunID:        run.ID,
			Slug:         "swallowed-error",
			Title:        "error swallowed",
			Category:     "fault_tolerance",
			Confidence:   "high",
			Severity:     "medium",
			ControlCodes: []string{"RC-021"},
			FilePath:     "errors.go",
			LineNo:       9,
			Fingerprint:  "loc-002",
		},
	}
	inserted, err := findRepo.CreateBatch(ctx, batch)
	if err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}
	if len(inserted) != 2 {
		t.Fatalf("CreateBatch: got %d rows, want 2", len(inserted))
	}
	for i, f := range inserted {
		if f.ID == uuid.Nil {
			t.Errorf("inserted[%d].ID is zero", i)
		}
		if f.OrgID != orgID {
			t.Errorf("inserted[%d].OrgID = %s, want %s", i, f.OrgID, orgID)
		}
	}

	listed, err := findRepo.ListByRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListByRun: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("ListByRun: got %d rows, want 2", len(listed))
	}
	// ORDER BY file_path: client.go before errors.go.
	if listed[0].FilePath != "client.go" || listed[1].FilePath != "errors.go" {
		t.Errorf("order: got %s,%s; want client.go,errors.go", listed[0].FilePath, listed[1].FilePath)
	}
}

func TestDetectionFinding_ExistsByDedupSignature(t *testing.T) {
	rls := newRLSPool(t)
	orgA := uuid.New()
	orgB := uuid.New()
	ctxA := database.WithRLSContext(context.Background(), "ua", orgA, nil)
	ctxB := database.WithRLSContext(context.Background(), "ub", orgB, nil)

	bindingA := seedRunPrereqs(t, rls, ctxA)
	_ = seedRunPrereqs(t, rls, ctxB)
	runRepo := NewDetectionRunRepo(rls)
	findRepo := NewDetectionFindingRepo(rls)

	run, err := runRepo.Create(ctxA, DetectionRun{
		BindingID: bindingA, Mode: DetectionModeFull, Phase: DetectionPhaseCompleted, FindingsTotal: 1,
	})
	if err != nil {
		t.Fatalf("Create run A: %v", err)
	}

	sig := "shared-signature-xyz"
	_, err = findRepo.CreateBatch(ctxA, []DetectionFinding{{
		RunID: run.ID, Slug: "missing-timeout", Title: "t",
		Category: "fault_tolerance", Confidence: "high", Severity: "high",
		ControlCodes: []string{"RC-018"}, FilePath: "client.go", LineNo: 11,
		Fingerprint: "loc-001", DedupSignature: &sig,
	}})
	if err != nil {
		t.Fatalf("CreateBatch A: %v", err)
	}

	// Org A: signature exists.
	gotA, err := findRepo.ExistsByDedupSignature(ctxA, sig)
	if err != nil {
		t.Fatalf("ExistsByDedupSignature A: %v", err)
	}
	if !gotA {
		t.Error("ExistsByDedupSignature(A, sig) = false; want true")
	}

	// Org B: same signature must NOT be visible (RLS isolation).
	gotB, err := findRepo.ExistsByDedupSignature(ctxB, sig)
	if err != nil {
		t.Fatalf("ExistsByDedupSignature B: %v", err)
	}
	if gotB {
		t.Error("RLS leak: orgB sees orgA's dedup_signature")
	}

	// Empty signature is the conservative "uniqueable" sentinel: never deduped.
	gotEmpty, err := findRepo.ExistsByDedupSignature(ctxA, "")
	if err != nil {
		t.Fatalf("ExistsByDedupSignature empty: %v", err)
	}
	if gotEmpty {
		t.Error("ExistsByDedupSignature(\"\") should always be false")
	}
}

func TestDetectionFinding_CascadeOnRunDelete(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)
	bindingID := seedRunPrereqs(t, rls, ctx)
	runRepo := NewDetectionRunRepo(rls)
	findRepo := NewDetectionFindingRepo(rls)

	run, err := runRepo.Create(ctx, DetectionRun{
		BindingID: bindingID, Mode: DetectionModeFull, Phase: DetectionPhaseCompleted, FindingsTotal: 1,
	})
	if err != nil {
		t.Fatalf("Create run: %v", err)
	}
	_, err = findRepo.CreateBatch(ctx, []DetectionFinding{{
		RunID: run.ID, Slug: "missing-timeout", Title: "t",
		Category: "fault_tolerance", Confidence: "high", Severity: "high",
		ControlCodes: []string{"RC-018"}, FilePath: "f.go", LineNo: 1, Fingerprint: "loc-1",
	}})
	if err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}

	// Delete the parent run.
	if _, err := rls.Exec(ctx, `DELETE FROM detection_runs WHERE id = $1`, run.ID); err != nil {
		t.Fatalf("delete run: %v", err)
	}
	// Findings should be gone via ON DELETE CASCADE.
	listed, err := findRepo.ListByRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListByRun after parent delete: %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("ON DELETE CASCADE didn't remove findings; %d remain", len(listed))
	}
}
