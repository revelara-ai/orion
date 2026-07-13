package orchestrator

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
)

// TestRatifySplitGuards (or-045a.4): the split is gated exactly like the other
// ratification acts — resolved type first, ratified goals as its source,
// blocking open questions answered, at least two non-empty sub-specs, and
// never twice.
func TestRatifySplitGuards(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, gameIntent); err != nil {
		t.Fatal(err)
	}
	subs := []SubIntent{{Name: "world", Intent: "world simulation"}, {Name: "ai", Intent: "RL enemy behavior"}}

	// Unresolved project type refuses the split (children inherit the type).
	if _, err := c.RatifySplit(ctx, subs); err == nil || !strings.Contains(err.Error(), "type") {
		t.Fatalf("an unclassified parent must refuse the split, got: %v", err)
	}
	if err := c.SetProjectType(ctx, "game"); err != nil {
		t.Fatal(err)
	}
	// The split derives from RATIFIED goals — nothing ratified, no split.
	if _, err := c.RatifySplit(ctx, subs); err == nil || !strings.Contains(err.Error(), "goals") {
		t.Fatalf("a split without ratified goals must be refused, got: %v", err)
	}
	if err := c.ProposeGoals(ctx, GoalsDoc{Goals: []string{"co-op PvE extraction"}}); err != nil {
		t.Fatal(err)
	}
	// A DRAFTED-but-unratified goals doc is not enough either — the split
	// derives from what the developer confirmed, not from a proposal.
	if _, err := c.RatifySplit(ctx, subs); err == nil || !strings.Contains(err.Error(), "goals") {
		t.Fatalf("a split on drafted (unratified) goals must be refused, got: %v", err)
	}
	if _, err := c.RatifyGoals(ctx); err != nil {
		t.Fatal(err)
	}
	// Fewer than two sub-specs is not a split.
	if _, err := c.RatifySplit(ctx, subs[:1]); err == nil {
		t.Fatal("a single sub-spec must be refused (that is a flat spec)")
	}
	// Empty sub-intents are refused.
	if _, err := c.RatifySplit(ctx, []SubIntent{{Name: "a", Intent: "x"}, {Name: "b"}}); err == nil {
		t.Fatal("an empty sub-intent must be refused")
	}
	// A blocking open question gates the split like every other ratification.
	qid, err := c.RaiseOpenQuestion(ctx, "spec", "grill", "", "How many sub-systems?", "blocking")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.RatifySplit(ctx, subs); err == nil || !strings.Contains(err.Error(), "open question") {
		t.Fatalf("a blocking open question must gate the split, got: %v", err)
	}
	if err := c.ResolveOpenQuestion(ctx, qid, "answered", "two"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RatifySplit(ctx, subs); err != nil {
		t.Fatalf("with type+goals+questions resolved the split must ratify: %v", err)
	}
	// Never twice: the active project after the split is CHILD one; a re-split
	// is refused (a child of a split cannot itself be split in this slice).
	if _, err := c.RatifySplit(ctx, subs); err == nil {
		t.Fatal("re-splitting must be refused")
	}
}

// TestSpecOfSpecsFlow (or-045a.4 DONE-WHEN b+e): a ratified split on a
// goals-ratified parent produces child projects that EACH run the normal
// intake to an independently ratified spec with its OWN anchor hash, inherit
// the parent's direction decisions and STPA model, chain through the active
// slot, and finally roll the parent up to delivered.
func TestSpecOfSpecsFlow(t *testing.T) {
	target := filepath.Join(t.TempDir(), "big-system")
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, largeHTTPIntent); err != nil {
		t.Fatal(err)
	}
	// Goal altitude first: ratified goals + goal-level STPA on the PARENT.
	if err := c.ProposeGoals(ctx, GoalsDoc{Goals: []string{"reliable time at scale"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RatifyGoals(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.RatifyGoalHazards(ctx,
		[]stpa.Loss{{ID: "GL1", Description: "clients act on wrong time"}},
		[]stpa.LossScenario{{ID: "GS1", Trigger: "clock skew", SustainingCondition: "no alarm", Loss: "GL1"}}); err != nil {
		t.Fatal(err)
	}
	// Direction ratified on the parent, inherited by every child.
	if err := c.RecordAnswer(ctx, "direction.language", "go"); err != nil {
		t.Fatal(err)
	}
	if err := c.RecordAnswer(ctx, "direction.repo_layout", target); err != nil {
		t.Fatal(err)
	}

	parent, _, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	children, err := c.RatifySplit(ctx, []SubIntent{
		{Name: "clock-core", Intent: "Build an HTTP service that returns the current time."},
		{Name: "clock-audit", Intent: "Build an HTTP service that returns the current time with an audit trail."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(children))
	}
	// Handoff: the first child takes the active slot; the parent waits queued.
	if children[0].Status != "active" || children[1].Status != "queued" {
		t.Fatalf("split handoff wrong: %q/%q", children[0].Status, children[1].Status)
	}
	if p := projectByID(t, c.Store(), parent.ID); p.Status != "queued" {
		t.Fatalf("the parent must leave the active slot (queued until roll-up), got %q", p.Status)
	}

	// Inheritance: direction decisions land in each child's spec lineage…
	childSpec := latestSpec(t, c.Store(), children[0].ID)
	ds, err := c.Store().DecisionsForSpec(ctx, childSpec.ID)
	if err != nil {
		t.Fatal(err)
	}
	inherited := map[string]string{}
	for _, d := range ds {
		if strings.HasPrefix(d.Key, "direction.") {
			inherited[d.Key] = d.Value
		}
	}
	if inherited["direction.language"] != "go" || inherited["direction.repo_layout"] != target {
		t.Fatalf("children must inherit the parent's direction decisions, got %+v", inherited)
	}
	// …and the parent's ratified STPA model is readable on the child.
	if m, ok, err := stpa.Load(ctx, c.Store(), children[0].ID); err != nil || !ok || len(m.Losses) != 1 || m.Losses[0].ID != "GL1" {
		t.Fatalf("children must inherit the parent's STPA model, got ok=%v m=%+v err=%v", ok, m, err)
	}

	// Child 1 runs the NORMAL intake to its own ratified spec.
	answerFunctional(t, c, ctx)
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatalf("child 1 spec must ratify independently: %v", err)
	}
	spec1 := latestSpec(t, c.Store(), children[0].ID)
	if spec1.Status != "accepted" || spec1.Hash == "" {
		t.Fatalf("child 1 must hold its own accepted spec + anchor hash: %+v", spec1)
	}
	// Cross-rail: the inherited repo_layout path persisted as the child's repo target.
	if p := projectByID(t, c.Store(), children[0].ID); p.RepoTarget != target {
		t.Fatalf("the inherited repo target must persist on the child, got %q", p.RepoTarget)
	}

	// Deliver child 1; the chain activates child 2.
	deliver(t, c.Store(), children[0].ID)
	next, rolled, err := c.Store().AdvanceSplit(ctx, children[0].ID)
	if err != nil || rolled || next.ID != children[1].ID {
		t.Fatalf("delivery must chain to the second child, got next=%+v rolled=%v err=%v", next, rolled, err)
	}

	// Child 2 ratifies its OWN spec with a DISTINCT anchor hash.
	answerFunctional(t, c, ctx)
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatalf("child 2 spec must ratify independently: %v", err)
	}
	spec2 := latestSpec(t, c.Store(), children[1].ID)
	if spec2.Hash == "" || spec2.Hash == spec1.Hash {
		t.Fatalf("each sub-spec carries its OWN anchor: %q vs %q", spec2.Hash, spec1.Hash)
	}

	// The LAST delivery rolls the parent up.
	deliver(t, c.Store(), children[1].ID)
	if _, rolled, err := c.Store().AdvanceSplit(ctx, children[1].ID); err != nil || !rolled {
		t.Fatalf("the last child delivery must roll the parent up, got rolled=%v err=%v", rolled, err)
	}
	if p := projectByID(t, c.Store(), parent.ID); p.Status != "delivered" {
		t.Fatalf("parent must be delivered after roll-up, got %q", p.Status)
	}

	// DONE-WHEN e: the tree renders parent + children with statuses.
	tree, err := c.ProjectTree(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"clock-core", "clock-audit", "delivered"} {
		if !strings.Contains(tree, want) {
			t.Fatalf("tree must show %q:\n%s", want, tree)
		}
	}
}

func projectByID(t *testing.T, st *contextstore.Store, id string) contextstore.Project {
	t.Helper()
	var p contextstore.Project
	if err := st.WithTx(t.Context(), func(tx *contextstore.Tx) error {
		var e error
		p, e = tx.Projects().Get(t.Context(), id)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	return p
}

func latestSpec(t *testing.T, st *contextstore.Store, projectID string) contextstore.Spec {
	t.Helper()
	var s contextstore.Spec
	if err := st.WithTx(t.Context(), func(tx *contextstore.Tx) error {
		var e error
		s, e = tx.Specs().LatestForProject(t.Context(), projectID)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func deliver(t *testing.T, st *contextstore.Store, id string) {
	t.Helper()
	if err := st.WithTx(t.Context(), func(tx *contextstore.Tx) error {
		return tx.Projects().SetStatus(t.Context(), id, "delivered")
	}); err != nil {
		t.Fatal(err)
	}
}
