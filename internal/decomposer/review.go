package decomposer

import (
	"context"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// ReviewFinding is one adversarial-review finding over the decomposed issue
// set (or-zn8, V3 Step 4). The reviewer PROPOSES; the deterministic
// IssueReviewGate in the orchestrator decides what a finding does.
type ReviewFinding struct {
	Severity  string   `json:"severity"`  // high | medium | low
	Dimension string   `json:"dimension"` // contradiction | coverage-gap | dependency-order | scope-collision | testability | operability
	Issues    []string `json:"issues"`    // task keys involved
	Concern   string   `json:"concern"`
}

// IssueReviewer adversarially reviews a decomposed epic against its spec.
// Assumed ADVERSARIAL AND FALLIBLE (an LLM): its findings are proposals — the
// gate applies the severity policy, corroboration, and rollout mode. The
// heart of the review is CROSS-ISSUE CONTRADICTION (issue A asserts X while
// issue B assumes not-X) — the class no per-issue check can see.
type IssueReviewer func(ctx context.Context, es spec.ExecutableSpec, epic Epic) ([]ReviewFinding, error)
