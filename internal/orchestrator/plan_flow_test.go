package orchestrator

import "testing"

// TestPlanViewDecomposesAcceptedSpec drives submit→answer→approve→PlanView and
// asserts the decomposition contract the `orion plan show` predicate checks:
// tasks exist, every task carries a (non-null, non-empty) ProofObligation and a
// file scope, and dependency edges reference real task ids.
func TestPlanViewDecomposesAcceptedSpec(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatalf("submit: %v", err)
	}
	answerFunctional(t, c, ctx)
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatalf("approve assumptions: %v", err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatalf("approve: %v", err)
	}

	pv, err := c.PlanView(ctx)
	if err != nil {
		t.Fatalf("plan view: %v", err)
	}
	if len(pv.Tasks) == 0 {
		t.Fatal("plan has no tasks")
	}
	ids := map[string]bool{}
	for _, task := range pv.Tasks {
		ids[task.ID] = true
	}
	for _, task := range pv.Tasks {
		if task.ProofObligation == "" {
			t.Fatalf("task %q (%s) has empty proof_obligation", task.Title, task.ID)
		}
		if task.FileScope == "" {
			t.Fatalf("task %q has empty file_scope", task.Title)
		}
		for _, dep := range task.DependsOn {
			if !ids[dep] {
				t.Fatalf("task %q depends on unknown task id %q", task.Title, dep)
			}
		}
	}
}

// TestPlanViewIsIdempotent: calling PlanView twice does not re-decompose (stable
// task set / ids).
func TestPlanViewIsIdempotent(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatalf("submit: %v", err)
	}
	answerFunctional(t, c, ctx)
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatalf("approve assumptions: %v", err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatalf("approve: %v", err)
	}
	first, err := c.PlanView(ctx)
	if err != nil {
		t.Fatalf("plan view 1: %v", err)
	}
	second, err := c.PlanView(ctx)
	if err != nil {
		t.Fatalf("plan view 2: %v", err)
	}
	if len(first.Tasks) != len(second.Tasks) {
		t.Fatalf("plan not idempotent: %d vs %d tasks", len(first.Tasks), len(second.Tasks))
	}
	for i := range first.Tasks {
		if first.Tasks[i].ID != second.Tasks[i].ID {
			t.Fatalf("task ids changed across PlanView calls (re-decomposed)")
		}
	}
}

// TestPlanViewRequiresAcceptedSpec: planning before approval errors.
func TestPlanViewRequiresAcceptedSpec(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := c.PlanView(ctx); err == nil {
		t.Fatal("expected PlanView to fail before the spec is accepted")
	}
}
