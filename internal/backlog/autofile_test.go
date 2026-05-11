package backlog

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/database"
	"github.com/revelara-ai/orion/internal/repos"
	"github.com/revelara-ai/orion/internal/trackers"
)

// recordingAdapter is a stub TrackerAdapter that records Create
// calls so the autofile tests can assert.
type recordingAdapter struct {
	mu          sync.Mutex
	creates     []trackers.IssueDraft
	createError error
}

func (a *recordingAdapter) Kind() trackers.TrackerKind { return trackers.TrackerKindGitHubIssues }
func (a *recordingAdapter) Capabilities() trackers.TrackerCapabilities {
	return trackers.TrackerCapabilities{CanCreate: true}
}
func (a *recordingAdapter) HealthCheck(context.Context, trackers.TrackerBinding) error { return nil }
func (a *recordingAdapter) FetchCandidates(context.Context, trackers.TrackerBinding, time.Time) ([]trackers.NormalizedIssue, error) {
	return nil, nil
}
func (a *recordingAdapter) FetchByExternalIDs(context.Context, trackers.TrackerBinding, []string) ([]trackers.NormalizedIssue, error) {
	return nil, nil
}
func (a *recordingAdapter) Create(_ context.Context, _ trackers.TrackerBinding, draft trackers.IssueDraft) (trackers.NormalizedIssue, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.creates = append(a.creates, draft)
	if a.createError != nil {
		return trackers.NormalizedIssue{}, a.createError
	}
	return trackers.NormalizedIssue{
		ExternalID: "gh:auto#1",
		Title:      draft.Title,
	}, nil
}
func (a *recordingAdapter) UpdateState(context.Context, trackers.TrackerBinding, string, trackers.NormalizedState) error {
	return nil
}
func (a *recordingAdapter) Comment(context.Context, trackers.TrackerBinding, string, string) error {
	return nil
}

// autofileFixture wires everything against a fresh pg container.
type autofileFixture struct {
	rls     *database.RLSPool
	orgID   uuid.UUID
	ctx     context.Context
	adapter *recordingAdapter
	counts  *repos.AutoFileCountsRepo
	binding repos.TrackerBinding
}

func newAutofileFixture(t *testing.T) *autofileFixture {
	t.Helper()
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)
	_, bindingID := seedRepoBinding(t, rls, ctx, "test/autofile")
	binding, err := repos.NewTrackerBindingRepo(rls).Get(ctx, bindingID)
	if err != nil {
		t.Fatalf("get binding: %v", err)
	}
	return &autofileFixture{
		rls:     rls,
		orgID:   orgID,
		ctx:     ctx,
		adapter: &recordingAdapter{},
		counts:  repos.NewAutoFileCountsRepo(rls),
		binding: *binding,
	}
}

func TestAutoFile_TrustModeShadowSkips(t *testing.T) {
	f := newAutofileFixture(t)
	g := &AutoFileGate{Adapter: f.adapter, Counts: f.counts}
	res, err := g.MaybeFile(f.ctx, "run-1", f.binding, TrustModeShadow, true, DefaultCapsFor(TrustModeShadow), Finding{Pattern: "p", Title: "t", Body: "b"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Filed {
		t.Error("shadow should NOT file")
	}
	if len(f.adapter.creates) != 0 {
		t.Errorf("adapter.Create called %d times, want 0", len(f.adapter.creates))
	}
}

func TestAutoFile_AutoFileFalseSkips(t *testing.T) {
	f := newAutofileFixture(t)
	g := &AutoFileGate{Adapter: f.adapter, Counts: f.counts}
	res, _ := g.MaybeFile(f.ctx, "run-1", f.binding, TrustModeFull, false, DefaultCapsFor(TrustModeFull), Finding{Pattern: "p", Title: "t", Body: "b"})
	if res.Filed {
		t.Error("auto_file=false should skip")
	}
}

func TestAutoFile_HappyPathFilesAndRecords(t *testing.T) {
	f := newAutofileFixture(t)
	g := &AutoFileGate{Adapter: f.adapter, Counts: f.counts}
	res, err := g.MaybeFile(f.ctx, "run-1", f.binding, TrustModeFull, true, DefaultCapsFor(TrustModeFull), Finding{
		Pattern: "missing_timeout",
		Title:   "Add timeout to /svc",
		Body:    "details",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !res.Filed {
		t.Errorf("expected Filed=true (reason=%q)", res.Reason)
	}
	if len(f.adapter.creates) != 1 {
		t.Errorf("adapter.Create called %d times, want 1", len(f.adapter.creates))
	}
	if len(f.adapter.creates) > 0 {
		labels := f.adapter.creates[0].Labels
		hasOrionFiled := false
		for _, l := range labels {
			if l == "orion-filed" {
				hasOrionFiled = true
				break
			}
		}
		if !hasOrionFiled {
			t.Errorf("draft labels=%v missing orion-filed", labels)
		}
	}
	// Cap record landed.
	n, err := f.counts.CountByRun(f.ctx, "run-1")
	if err != nil {
		t.Fatalf("CountByRun: %v", err)
	}
	if n != 1 {
		t.Errorf("CountByRun=%d, want 1", n)
	}
}

func TestAutoFile_PerRunCapReached(t *testing.T) {
	f := newAutofileFixture(t)
	g := &AutoFileGate{Adapter: f.adapter, Counts: f.counts}
	caps := AutoFileCaps{MaxPerRun: 2, MaxPer24h: 100, WindowSize: 24 * time.Hour}
	// Fill the run.
	for i := 0; i < 2; i++ {
		res, err := g.MaybeFile(f.ctx, "run-cap", f.binding, TrustModeFull, true, caps, Finding{Pattern: "p", Title: "t", Body: "b"})
		if err != nil || !res.Filed {
			t.Fatalf("iter %d filed=%v err=%v", i, res.Filed, err)
		}
	}
	// Third call hits the cap.
	res, _ := g.MaybeFile(f.ctx, "run-cap", f.binding, TrustModeFull, true, caps, Finding{Pattern: "p", Title: "t", Body: "b"})
	if res.Filed {
		t.Error("third call should be capped, not filed")
	}
	if res.Reason == "" || len(f.adapter.creates) != 2 {
		t.Errorf("creates=%d (want 2), reason=%q", len(f.adapter.creates), res.Reason)
	}
}

func TestAutoFile_DedupHitSkips(t *testing.T) {
	f := newAutofileFixture(t)
	g := &AutoFileGate{
		Adapter: f.adapter,
		Counts:  f.counts,
		CheckDedup: func(_ context.Context, sig string) (bool, error) {
			return sig == "dup-sig", nil
		},
	}
	res, err := g.MaybeFile(f.ctx, "run-1", f.binding, TrustModeFull, true, DefaultCapsFor(TrustModeFull), Finding{
		Pattern:        "p",
		Title:          "t",
		Body:           "b",
		DedupSignature: "dup-sig",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Filed {
		t.Error("dedup hit should skip filing")
	}
}

func TestAutoFile_LowPatternTrustSkips(t *testing.T) {
	f := newAutofileFixture(t)
	g := &AutoFileGate{
		Adapter:           f.adapter,
		Counts:            f.counts,
		PatternTrustAbove: func(p string) bool { return p != "untrusted" },
	}
	res, _ := g.MaybeFile(f.ctx, "run-1", f.binding, TrustModeFull, true, DefaultCapsFor(TrustModeFull), Finding{Pattern: "untrusted", Title: "t", Body: "b"})
	if res.Filed {
		t.Error("low pattern trust should skip")
	}
	res2, _ := g.MaybeFile(f.ctx, "run-1", f.binding, TrustModeFull, true, DefaultCapsFor(TrustModeFull), Finding{Pattern: "trusted", Title: "t", Body: "b"})
	if !res2.Filed {
		t.Errorf("trusted pattern should file, got reason=%q", res2.Reason)
	}
}

func TestAutoFile_AdapterCreateErrorSurfaces(t *testing.T) {
	f := newAutofileFixture(t)
	f.adapter.createError = errors.New("upstream 500")
	g := &AutoFileGate{Adapter: f.adapter, Counts: f.counts}
	_, err := g.MaybeFile(f.ctx, "run-1", f.binding, TrustModeFull, true, DefaultCapsFor(TrustModeFull), Finding{Pattern: "p", Title: "t", Body: "b"})
	if err == nil {
		t.Fatal("expected error from adapter")
	}
}
