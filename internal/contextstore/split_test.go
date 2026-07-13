package contextstore

import (
	"context"
	"strings"
	"testing"
)

func splitStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// splitFixture creates an active parent with n queued children and returns
// (parentID, childIDs).
func splitFixture(t *testing.T, st *Store, n int) (string, []string) {
	t.Helper()
	ctx := context.Background()
	var parent string
	kids := make([]string, 0, n)
	if err := st.WithTx(ctx, func(tx *Tx) error {
		var err error
		parent, err = tx.Projects().Create(ctx, "parent", "build a big game", "game")
		if err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			id, err := tx.Projects().CreateChild(ctx, parent, "sub", "sub-spec "+string(rune('A'+i)), "game")
			if err != nil {
				return err
			}
			kids = append(kids, id)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return parent, kids
}

func setStatus(t *testing.T, st *Store, id, status string) error {
	t.Helper()
	return st.WithTx(context.Background(), func(tx *Tx) error {
		return tx.Projects().SetStatus(context.Background(), id, status)
	})
}

func getProject(t *testing.T, st *Store, id string) Project {
	t.Helper()
	var p Project
	if err := st.WithTx(context.Background(), func(tx *Tx) error {
		var e error
		p, e = tx.Projects().Get(context.Background(), id)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCreateChildQueued (or-045a.4 DONE-WHEN a): a sub-spec child project is
// created QUEUED under its parent (single-active invariant untouched),
// inherits the parent's resolved type at STANDARD scale, and the global FIFO
// intent queue is unaffected.
func TestCreateChildQueued(t *testing.T) {
	st := splitStore(t)
	ctx := context.Background()

	// An unrelated queued intent exists BEFORE the split (FIFO head).
	var other string
	if err := st.WithTx(ctx, func(tx *Tx) error {
		var e error
		other, e = tx.Projects().Create(ctx, "other", "unrelated intent", "cli")
		if e != nil {
			return e
		}
		return tx.Projects().SetStatus(ctx, other, "queued")
	}); err != nil {
		t.Fatal(err)
	}

	parent, kids := splitFixture(t, st, 2)
	if len(kids) != 2 {
		t.Fatalf("expected 2 children, got %d", len(kids))
	}
	var children []Project
	if err := st.WithTx(ctx, func(tx *Tx) error {
		var e error
		children, e = tx.Projects().ChildrenOf(ctx, parent)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if len(children) != 2 || children[0].ID != kids[0] || children[1].ID != kids[1] {
		t.Fatalf("ChildrenOf must return the children in creation order: %+v", children)
	}
	for _, c := range children {
		if c.Status != "queued" {
			t.Fatalf("a child must be created QUEUED (never a second active), got %q", c.Status)
		}
		if c.ParentProjectID != parent {
			t.Fatalf("child must carry its parent id, got %q", c.ParentProjectID)
		}
		if c.ProjectType != "game" {
			t.Fatalf("child must inherit the parent's resolved type, got %q", c.ProjectType)
		}
		if c.Scale != "standard" {
			t.Fatalf("a sub-spec is feature-sized: scale must be standard, got %q", c.Scale)
		}
	}
	// The global FIFO is intact: the pre-existing queued intent is still the head.
	var head Project
	if err := st.WithTx(ctx, func(tx *Tx) error {
		var e error
		head, e = tx.Projects().OldestQueued(ctx)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if head.ID != other {
		t.Fatalf("queue ordering must be intact (FIFO head unchanged), got %q want %q", head.ID, other)
	}
	// Negative: a project with no children reports none.
	if err := st.WithTx(ctx, func(tx *Tx) error {
		cs, e := tx.Projects().ChildrenOf(ctx, other)
		if e != nil {
			return e
		}
		if len(cs) != 0 {
			t.Fatalf("a childless project must report no children: %+v", cs)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestDeliveryRollUpGate (or-045a.4 DONE-WHEN c+d): the parent of a
// spec-of-specs cannot transition to delivered while ANY child is undelivered
// — and a project with no children delivers exactly as before.
func TestDeliveryRollUpGate(t *testing.T) {
	st := splitStore(t)
	parent, kids := splitFixture(t, st, 2)

	if err := setStatus(t, st, parent, "delivered"); err == nil || !strings.Contains(err.Error(), "sub-spec") {
		t.Fatalf("a parent with undelivered children must refuse delivery, got: %v", err)
	}
	if err := setStatus(t, st, kids[0], "delivered"); err != nil {
		t.Fatal(err)
	}
	// One child delivered is not enough — EVERY sub-spec is a feature without
	// which the main spec is unfulfilled.
	if err := setStatus(t, st, parent, "delivered"); err == nil {
		t.Fatal("one remaining undelivered child must still block the parent")
	}
	if err := setStatus(t, st, kids[1], "delivered"); err != nil {
		t.Fatal(err)
	}
	if err := setStatus(t, st, parent, "delivered"); err != nil {
		t.Fatalf("with every child delivered the parent must deliver: %v", err)
	}
	if got := getProject(t, st, parent).Status; got != "delivered" {
		t.Fatalf("parent status = %q, want delivered", got)
	}

	// DONE-WHEN d: no children ⇒ identical behavior.
	st2 := splitStore(t)
	ctx := context.Background()
	var single string
	if err := st2.WithTx(ctx, func(tx *Tx) error {
		var e error
		single, e = tx.Projects().Create(ctx, "single", "one flat spec", "http-service")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err := setStatus(t, st2, single, "delivered"); err != nil {
		t.Fatalf("a childless project must deliver unchanged: %v", err)
	}

	// An ABANDONED child also blocks: the feature is missing, the main spec is
	// not fulfilled (abandon the parent instead if the split is dead).
	parent2, kids2 := splitFixture(t, st, 1)
	if err := setStatus(t, st, kids2[0], "abandoned"); err != nil {
		t.Fatal(err)
	}
	if err := setStatus(t, st, parent2, "delivered"); err == nil {
		t.Fatal("an abandoned child must block parent delivery (the feature is missing)")
	}
}

// TestAdvanceSplit (or-045a.4): after a child delivers, AdvanceSplit chains the
// oldest queued sibling into the active slot; when the LAST child delivers it
// rolls the parent up to delivered. Non-children are a no-op.
func TestAdvanceSplit(t *testing.T) {
	st := splitStore(t)
	ctx := context.Background()
	parent, kids := splitFixture(t, st, 3)
	// Simulate the ratified-split handoff: parent leaves the slot, first child runs.
	if err := setStatus(t, st, parent, "queued"); err != nil {
		t.Fatal(err)
	}
	if err := setStatus(t, st, kids[0], "active"); err != nil {
		t.Fatal(err)
	}

	if err := setStatus(t, st, kids[0], "delivered"); err != nil {
		t.Fatal(err)
	}
	next, rolled, err := st.AdvanceSplit(ctx, kids[0])
	if err != nil || rolled {
		t.Fatalf("siblings remain: must chain, not roll up (next=%+v rolled=%v err=%v)", next, rolled, err)
	}
	if next.ID != kids[1] || getProject(t, st, kids[1]).Status != "active" {
		t.Fatalf("the oldest queued sibling must activate, got %+v", next)
	}

	if err := setStatus(t, st, kids[1], "delivered"); err != nil {
		t.Fatal(err)
	}
	if next, _, err = st.AdvanceSplit(ctx, kids[1]); err != nil || next.ID != kids[2] {
		t.Fatalf("chain must continue to the third child, got %+v err=%v", next, err)
	}

	if err := setStatus(t, st, kids[2], "delivered"); err != nil {
		t.Fatal(err)
	}
	next, rolled, err = st.AdvanceSplit(ctx, kids[2])
	if err != nil {
		t.Fatal(err)
	}
	if !rolled || next.ID != parent {
		t.Fatalf("the last delivery must roll the parent up, got next=%+v rolled=%v", next, rolled)
	}
	if got := getProject(t, st, parent).Status; got != "delivered" {
		t.Fatalf("rolled-up parent status = %q, want delivered", got)
	}

	// Negative: a standalone project is a no-op (no error, nothing activated).
	var single string
	if err := st.WithTx(ctx, func(tx *Tx) error {
		var e error
		single, e = tx.Projects().Create(ctx, "solo", "flat intent", "cli")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err := setStatus(t, st, single, "delivered"); err != nil {
		t.Fatal(err)
	}
	if next, rolled, err := st.AdvanceSplit(ctx, single); err != nil || rolled || next.ID != "" {
		t.Fatalf("a non-child delivery must be a no-op, got next=%+v rolled=%v err=%v", next, rolled, err)
	}

	// Negative: an abandoned sibling stops the roll-up — the parent must NOT
	// silently deliver around a missing feature.
	parent2, kids2 := splitFixture(t, st, 2)
	if err := setStatus(t, st, parent2, "queued"); err != nil {
		t.Fatal(err)
	}
	if err := setStatus(t, st, kids2[0], "abandoned"); err != nil {
		t.Fatal(err)
	}
	if err := setStatus(t, st, kids2[1], "delivered"); err != nil {
		t.Fatal(err)
	}
	if _, rolled, err := st.AdvanceSplit(ctx, kids2[1]); err != nil || rolled {
		t.Fatalf("an abandoned sibling must block the roll-up, got rolled=%v err=%v", rolled, err)
	}
	if got := getProject(t, st, parent2).Status; got != "queued" {
		t.Fatalf("blocked parent must stay queued, got %q", got)
	}
}
