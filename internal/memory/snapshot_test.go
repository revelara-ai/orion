package memory

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMemorySnapshotToRoundTrip (or-hy4): the memory store snapshots to a
// restorable single file — an item written before the snapshot is retrievable
// from the snapshot opened as a store; one written after is not (point-in-time).
func TestMemorySnapshotToRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	if _, err := s.Write(ctx, Item{Tier: LTM, Kind: "fact", Content: "pre-snapshot fact", TrustTier: TrustHuman, Heat: 1}); err != nil {
		t.Fatal(err)
	}
	snapDir := t.TempDir()
	if err := s.SnapshotTo(ctx, filepath.Join(snapDir, "memory.db")); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if _, err := s.Write(ctx, Item{Tier: LTM, Kind: "fact", Content: "post-snapshot fact", TrustTier: TrustHuman, Heat: 1}); err != nil {
		t.Fatal(err)
	}

	restored, err := Open(snapDir)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer func() { _ = restored.Close() }()
	items, err := restored.Retrieve(ctx, "fact", LTM)
	if err != nil {
		t.Fatal(err)
	}
	var pre, post bool
	for _, it := range items {
		if it.Content == "pre-snapshot fact" {
			pre = true
		}
		if it.Content == "post-snapshot fact" {
			post = true
		}
	}
	if !pre {
		t.Fatalf("pre-snapshot item must round-trip, got %d items", len(items))
	}
	if post {
		t.Fatal("a post-snapshot write leaked into the snapshot — not point-in-time")
	}
}
