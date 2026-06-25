package conductor

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/worktree"
)

// TestFreshChangeIDAvoidsCollision: re-running the same intent must not wedge on the
// slug's worktree path/branch (the first dogfood finding, or-3p5.7). freshChangeID
// returns the base when free, then a distinct suffixed id once it is occupied.
func TestFreshChangeIDAvoidsCollision(t *testing.T) {
	repo := gitInitGreenRepo(t)
	mgr := worktree.New(repo, nil)
	ctx := context.Background()
	base := "orion-change-test"

	id1 := freshChangeID(ctx, mgr, repo, base)
	if id1 != base {
		t.Fatalf("first id = %q, want %q (base is free)", id1, base)
	}
	if _, err := mgr.Create(ctx, id1, "HEAD"); err != nil {
		t.Fatalf("create id1: %v", err)
	}

	id2 := freshChangeID(ctx, mgr, repo, base)
	if id2 == base {
		t.Fatalf("second id %q collided with the occupied base", id2)
	}
	if _, err := mgr.Create(ctx, id2, "HEAD"); err != nil {
		t.Fatalf("create id2 must succeed without collision: %v", err)
	}
}
