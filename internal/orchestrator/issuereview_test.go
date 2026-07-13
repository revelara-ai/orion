package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

func contradictionFinding(sev string) decomposer.ReviewFinding {
	return decomposer.ReviewFinding{
		Severity:  sev,
		Dimension: "contradiction",
		Issues:    []string{"handler", "security"},
		Concern:   "handler assumes anonymous access; security requires authenticated routes",
	}
}

func reviewerReturning(batches ...[]decomposer.ReviewFinding) decomposer.IssueReviewer {
	i := 0
	return func(context.Context, spec.ExecutableSpec, decomposer.Epic) ([]decomposer.ReviewFinding, error) {
		if i >= len(batches) {
			return nil, nil
		}
		b := batches[i]
		i++
		return b, nil
	}
}

func openReviewRows(t *testing.T, c *Conductor, ctx context.Context) []string {
	t.Helper()
	var rows []string
	_ = c.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		open, e := tx.Escalations().ListOpen(ctx)
		if e != nil {
			return e
		}
		for _, esc := range open {
			if strings.Contains(esc.Reason, "issue-review") {
				rows = append(rows, esc.Reason+": "+esc.Detail)
			}
		}
		return nil
	})
	return rows
}

// TestIssueReviewAdvisoryRecordsAndProceeds (or-zn8): the default posture —
// findings are SURFACED (inbox + log), the plan still lands. Advisory can
// never add or remove a green light.
func TestIssueReviewAdvisoryRecordsAndProceeds(t *testing.T) {
	c, ctx := storeConductor(t)
	c.SetIssueReviewer(reviewerReturning([]decomposer.ReviewFinding{contradictionFinding("high")}))
	driveToPlan(t, c, ctx)
	if _, err := c.PlanView(ctx); err != nil {
		t.Fatalf("advisory review must not block the plan: %v", err)
	}
	rows := openReviewRows(t, c, ctx)
	if len(rows) == 0 {
		t.Fatal("a high finding must land in the inbox even in advisory mode")
	}
	if !strings.Contains(rows[0], "anonymous access") {
		t.Fatalf("the finding's concern must be recorded, got %v", rows)
	}
}

// TestIssueReviewBlocksOnCorroboratedHigh (or-zn8, V3 Step 4): with
// ORION_ISSUE_REVIEW=block, a HIGH finding blocks the plan ONLY when a second
// independent pass corroborates it (same dimension — the G5 anti-flake rule);
// the error names the contradiction so the developer can patch the spec.
func TestIssueReviewBlocksOnCorroboratedHigh(t *testing.T) {
	t.Setenv("ORION_ISSUE_REVIEW", "block")
	c, ctx := storeConductor(t)
	c.SetIssueReviewer(reviewerReturning(
		[]decomposer.ReviewFinding{contradictionFinding("high")},
		[]decomposer.ReviewFinding{contradictionFinding("high")}, // corroboration pass agrees
	))
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatal(err)
	}
	answerFunctional(t, c, ctx)
	if _, err := c.ApproveAssumptions(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ApproveSpec(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := c.PlanView(ctx)
	if err == nil || !errors.Is(err, ErrIssueReviewBlocked) {
		t.Fatalf("a corroborated high contradiction must block the plan, got %v", err)
	}
	if !strings.Contains(err.Error(), "anonymous access") {
		t.Fatalf("the block must name the concern, got %v", err)
	}
}

// TestIssueReviewUncorroboratedHighDowngrades: the second pass disagrees — the
// finding downgrades to a surfaced medium (a non-deterministic judge must not
// flaky-block), and the plan proceeds.
func TestIssueReviewUncorroboratedHighDowngrades(t *testing.T) {
	t.Setenv("ORION_ISSUE_REVIEW", "block")
	c, ctx := storeConductor(t)
	c.SetIssueReviewer(reviewerReturning(
		[]decomposer.ReviewFinding{contradictionFinding("high")},
		nil, // corroboration pass finds nothing
	))
	driveToPlan(t, c, ctx)
	if _, err := c.PlanView(ctx); err != nil {
		t.Fatalf("an uncorroborated high must not block, got %v", err)
	}
	if len(openReviewRows(t, c, ctx)) == 0 {
		t.Fatal("the downgraded finding must still be surfaced to the human")
	}
}

// TestIssueReviewFailsOpen: a reviewer error or panic never fails the plan —
// the review is evidence-gathering; only its verified verdict gates.
func TestIssueReviewFailsOpen(t *testing.T) {
	c, ctx := storeConductor(t)
	c.SetIssueReviewer(func(context.Context, spec.ExecutableSpec, decomposer.Epic) ([]decomposer.ReviewFinding, error) {
		panic("reviewer exploded")
	})
	driveToPlan(t, c, ctx)
	if _, err := c.PlanView(ctx); err != nil {
		t.Fatalf("a panicking reviewer must never fail the plan: %v", err)
	}
}
