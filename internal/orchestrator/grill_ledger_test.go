package orchestrator

import (
	"context"
	"reflect"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

// TestGrillQuestionsPersistOnce (or-kjcm.3): grill questions are generated
// once per answer-set and served from the open-questions ledger afterwards —
// consecutive gate reads with no new answers produce identical question sets
// and no additional grill calls.
func TestGrillQuestionsPersistOnce(t *testing.T) {
	t.Setenv("ORION_ELICITATION", "grill")
	c, ctx := storeConductor(t)
	grill, calls := grillReturning([]completeness.OpenDecision{
		{Key: "grill.success", Question: "What does success look like?"},
		{Key: "grill.out_of_scope", Question: "What is explicitly OUT of scope?"},
	}, nil)
	c.SetGrillAgent(grill)

	conf, err := c.Submit(ctx, flowIntent)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("submit must consult the grill exactly once, calls=%d", *calls)
	}

	v1, err := c.SpecView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := c.SpecView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("gate reads with unchanged answers must not re-consult the grill, calls=%d", *calls)
	}
	if !reflect.DeepEqual(v1.OpenDecisions, v2.OpenDecisions) {
		t.Fatalf("consecutive gate reads must be identical:\n1: %+v\n2: %+v", v1.OpenDecisions, v2.OpenDecisions)
	}
	if v1.OpenDecisions[0].Key != "grill.success" || v1.OpenDecisions[1].Key != "grill.out_of_scope" {
		t.Fatalf("grill questions must lead: %+v", v1.OpenDecisions[:2])
	}
	if conf.OpenDecisions[0].Key != "grill.success" {
		t.Fatalf("submit's view must match: %+v", conf.OpenDecisions[0])
	}

	// The questions are first-class ledger rows, visible to the open-question
	// tools with their origin.
	qs, err := c.OpenQuestions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]contextstore.OpenQuestion{}
	for _, q := range qs {
		byKey[q.Key] = q
	}
	g, ok := byKey["grill.success"]
	if !ok {
		t.Fatalf("grill questions must land in the open-questions ledger: %+v", qs)
	}
	if g.Origin != "grill" || g.Phase != "spec" || g.Severity != "advisory" {
		t.Fatalf("ledger row shape: %+v", g)
	}
}

// TestGrillReconsultedAfterNewAnswer: a new recorded answer changes the
// answer-set, so the next gate read consults the grill again; the answered
// question is retired from the ledger and never resurfaces.
func TestGrillReconsultedAfterNewAnswer(t *testing.T) {
	t.Setenv("ORION_ELICITATION", "grill")
	c, ctx := storeConductor(t)
	// The fake always re-proposes the same questions — the ledger's dedup is
	// what must keep an answered question from coming back.
	grill, calls := grillReturning([]completeness.OpenDecision{
		{Key: "grill.success", Question: "What does success look like?"},
	}, nil)
	c.SetGrillAgent(grill)

	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}
	if err := c.RecordAnswer(ctx, "grill.success", "nobody asks for the time in slack"); err != nil {
		t.Fatal(err)
	}

	v, err := c.SpecView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Fatalf("a changed answer-set must re-consult the grill, calls=%d", *calls)
	}
	for _, d := range v.OpenDecisions {
		if d.Key == "grill.success" {
			t.Fatalf("an answered grill question must never reappear: %+v", v.OpenDecisions)
		}
	}
	// The ledger row is resolved, not still open.
	qs, err := c.OpenQuestions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range qs {
		if q.Key == "grill.success" {
			t.Fatalf("record_answer must retire the ledger question: %+v", q)
		}
	}
	// And no duplicate row was created by the re-consult.
	var all []contextstore.OpenQuestion
	if err := c.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		all, e = tx.OpenQuestions().ListByProject(ctx, c.ProjectID())
		return e
	}); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, q := range all {
		if q.Key == "grill.success" {
			n++
			if q.Status != "answered" {
				t.Fatalf("expected the answered status on the retired question: %+v", q)
			}
		}
	}
	if n != 1 {
		t.Fatalf("dedup must hold across re-consults, got %d rows for grill.success", n)
	}
}

// TestGrillLedgerSurvivesRestart: a fresh conductor over the same store serves
// the persisted questions; the re-consult (new process, empty fingerprint
// cache) must not duplicate them.
func TestGrillLedgerSurvivesRestart(t *testing.T) {
	t.Setenv("ORION_ELICITATION", "grill")
	st, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()

	c1 := NewWithStore(st)
	grill1, _ := grillReturning([]completeness.OpenDecision{
		{Key: "grill.success", Question: "What does success look like?"},
	}, nil)
	c1.SetGrillAgent(grill1)
	if _, err := c1.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}

	c2 := NewWithStore(st)
	grill2, _ := grillReturning([]completeness.OpenDecision{
		{Key: "grill.success", Question: "What does success look like?"},
	}, nil)
	c2.SetGrillAgent(grill2)
	v, err := c2.SpecView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v.OpenDecisions[0].Key != "grill.success" {
		t.Fatalf("the persisted grill question must lead after a restart: %+v", v.OpenDecisions)
	}
	var all []contextstore.OpenQuestion
	if err := st.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		all, e = tx.OpenQuestions().ListByProject(ctx, c1.ProjectID())
		return e
	}); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, q := range all {
		if q.Key == "grill.success" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("a restart's re-consult must dedup against the ledger, got %d rows", n)
	}
}

// TestGrillErrorFallsBackToLedgerAndFloor: once questions are persisted, a
// later grill failure serves the durable ledger + floor instead of dropping
// to the bare floor.
func TestGrillErrorFallsBackToLedgerAndFloor(t *testing.T) {
	t.Setenv("ORION_ELICITATION", "grill")
	c, ctx := storeConductor(t)
	grill, _ := grillReturning([]completeness.OpenDecision{
		{Key: "grill.success", Question: "What does success look like?"},
	}, nil)
	c.SetGrillAgent(grill)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}

	// The brain goes offline; an unrelated answer changes the answer-set so
	// the next read re-consults — and fails.
	broken, _ := grillReturning(nil, context.DeadlineExceeded)
	c.SetGrillAgent(broken)
	if err := c.RecordAnswer(ctx, "port", "8080"); err != nil {
		t.Fatal(err)
	}
	v, err := c.SpecView(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v.OpenDecisions[0].Key != "grill.success" {
		t.Fatalf("persisted questions must survive a grill outage: %+v", v.OpenDecisions)
	}
	var floorSeen bool
	for _, d := range v.OpenDecisions {
		if d.Key == "response_format" {
			floorSeen = true
		}
	}
	if !floorSeen {
		t.Fatal("the demoted floor must still surface during a grill outage")
	}
}
