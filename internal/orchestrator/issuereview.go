package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// ErrIssueReviewBlocked (or-zn8, V3 Step 4): a corroborated high-severity
// issue-set finding (typically a cross-issue contradiction) blocked the plan
// in blocking mode. The recovery is to patch the SPEC (amend_spec) — the
// contradiction lives in what was ratified, not in the decomposer.
var ErrIssueReviewBlocked = errors.New("issue review blocked the plan")

// reviewPlanGate is the IssueReviewGate — the gate V2 never had: an
// adversarial review over the DECOMPOSED ISSUE SET, hunting what per-issue
// checks structurally cannot see (cross-issue contradictions above all; then
// coverage gaps, dependency-order and scope collisions, untestable
// obligations).
//
// Split of duties (V3 §2): the reviewer (LLM) PROPOSES findings; this
// deterministic gate decides what they do. Default posture is ADVISORY —
// high/medium findings land in the unified inbox, the plan proceeds. With
// ORION_ISSUE_REVIEW=block, a HIGH finding blocks ONLY when an independent
// second pass corroborates its dimension (the AlignmentGate's G5 anti-flake
// rule); an uncorroborated high downgrades to a surfaced medium. Like every
// V3 gate it can only ever REMOVE a green light. A reviewer error or panic
// fails OPEN (advisory evidence lost, never a failed plan).
func (c *Conductor) reviewPlanGate(ctx context.Context, projID string, es spec.ExecutableSpec, epic decomposer.Epic) (err error) {
	if c.reviewer == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			c.log.WarnContext(ctx, "issue review: recovered from panic — plan proceeds unreviewed", "panic", r)
			err = nil
		}
	}()
	findings, rerr := c.reviewer(ctx, es, epic)
	if rerr != nil {
		c.log.WarnContext(ctx, "issue review: reviewer failed — plan proceeds unreviewed", "err", rerr)
		return nil
	}
	blocking := os.Getenv("ORION_ISSUE_REVIEW") == "block"
	var blockers []decomposer.ReviewFinding
	for _, f := range findings {
		sev := strings.ToLower(strings.TrimSpace(f.Severity))
		if sev == "high" && blocking {
			// G5 corroboration: a second independent pass must re-find the same
			// DIMENSION, else the high downgrades to a surfaced medium.
			if corroborated(ctx, c.reviewer, es, epic, f.Dimension) {
				blockers = append(blockers, f)
				continue
			}
			sev = "medium"
		}
		switch sev {
		case "high", "medium":
			c.recordReviewFinding(ctx, projID, f, sev)
		default:
			c.log.InfoContext(ctx, "issue review (low)", "dimension", f.Dimension, "concern", f.Concern)
		}
	}
	if len(blockers) > 0 {
		var concerns []string
		for _, f := range blockers {
			c.recordReviewFinding(ctx, projID, f, "high")
			concerns = append(concerns, fmt.Sprintf("[%s: %s] %s", f.Dimension, strings.Join(f.Issues, "+"), f.Concern))
		}
		return fmt.Errorf("%w: %s — patch the spec (amend_spec) and re-ratify", ErrIssueReviewBlocked, strings.Join(concerns, "; "))
	}
	return nil
}

func corroborated(ctx context.Context, reviewer decomposer.IssueReviewer, es spec.ExecutableSpec, epic decomposer.Epic, dimension string) bool {
	again, err := reviewer(ctx, es, epic)
	if err != nil {
		return false // an erroring corroboration pass never upholds a block
	}
	for _, f := range again {
		if strings.EqualFold(strings.TrimSpace(f.Severity), "high") && strings.EqualFold(f.Dimension, dimension) {
			return true
		}
	}
	return false
}

func (c *Conductor) recordReviewFinding(ctx context.Context, projID string, f decomposer.ReviewFinding, sev string) {
	_ = c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		_, e := tx.Escalations().CreateDetailed(ctx, projID, "",
			fmt.Sprintf("issue-review (%s): %s", sev, f.Dimension),
			fmt.Sprintf("issues: %s\n%s", strings.Join(f.Issues, ", "), f.Concern))
		return e
	})
}

// SetIssueReviewer injects the adversarial issue-set reviewer (or-zn8). Safe
// to leave nil — the gate is a no-op without one.
func (c *Conductor) SetIssueReviewer(r decomposer.IssueReviewer) { c.reviewer = r }
