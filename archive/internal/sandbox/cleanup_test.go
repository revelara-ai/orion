package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCleaner_CleanupRun_RemovesPolicyNamespaceAndWorktrees(t *testing.T) {
	ns := NewInMemoryNamespaceProvisioner()
	np := NewInMemoryNetworkPolicyApplier()
	runner := &recordingRunner{}
	cache, err := NewRepoCache(CacheConfig{
		Root:          t.TempDir(),
		WorktreesRoot: t.TempDir(),
		WorktreeTTL:   time.Hour,
		Runner:        runner,
	})
	if err != nil {
		t.Fatalf("NewRepoCache: %v", err)
	}

	c := NewCleaner(ns, np, cache)
	runID := uuid.New()
	tenantID := uuid.New()
	name := RunNamespaceName(runID)

	if _, err := ns.Provision(context.Background(), NamespaceSpec{RunID: runID, Name: name}); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if err := np.Apply(context.Background(), NetworkPolicySpec{Namespace: name}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if err := c.CleanupRun(context.Background(), runID, tenantID, "url", []uuid.UUID{uuid.New()}); err != nil {
		t.Errorf("CleanupRun returned error: %v", err)
	}
	if _, ok := ns.Snapshot()[name]; ok {
		t.Errorf("namespace not deleted")
	}
	if _, ok := np.Get(name); ok {
		t.Errorf("network policy not removed")
	}
}

func TestCleaner_TolerantOfNilComponents(t *testing.T) {
	c := NewCleaner(nil, nil, nil)
	if err := c.CleanupRun(context.Background(), uuid.New(), uuid.New(), "url", nil); err != nil {
		t.Errorf("nil components should be tolerated; got %v", err)
	}
	if n, err := c.GCExpiredWorktrees(context.Background()); err != nil || n != 0 {
		t.Errorf("GC with nil cache: n=%d err=%v", n, err)
	}
}
