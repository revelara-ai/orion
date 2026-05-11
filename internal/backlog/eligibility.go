package backlog

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/repos"
	"github.com/revelara-ai/orion/internal/trackers"
)

// EligibilityConfig is the per-repo configuration that drives rules
// 3-5 (label, path, branch). Defaults from SPEC §8.4 apply when the
// caller leaves a field empty.
type EligibilityConfig struct {
	// IneligibleLabels: labels that disqualify an issue from being
	// dispatched. Default: do-not-touch, human-only, human-only-design.
	IneligibleLabels []string

	// IneligiblePaths: file path substrings; if the issue body
	// references any of these, the issue is ineligible. Default:
	// auth, billing, payments.
	IneligiblePaths []string

	// IneligibleBranches: branch names; an issue declaring a target
	// branch in this set is ineligible. Default: empty.
	IneligibleBranches []string
}

// DefaultEligibilityConfig returns the §8.4 v1 defaults.
func DefaultEligibilityConfig() EligibilityConfig {
	return EligibilityConfig{
		IneligibleLabels: []string{"do-not-touch", "human-only", "human-only-design"},
		IneligiblePaths:  []string{"auth", "billing", "payments"},
	}
}

// AnnotationLookup is the rule-10 hook. Production wires
// dedup.LookupAnnotation against a parsed file; tests inject a stub.
// Returns true when the issue's referenced callsite is suppressed.
type AnnotationLookup func(ctx context.Context, issue trackers.NormalizedIssue) bool

// Evaluator applies SPEC §8.4 rules 1-10 to a NormalizedIssue and
// returns the resulting Eligibility enum value.
type Evaluator struct {
	Config              EligibilityConfig
	Preflight           *PreflightAssessor
	HasOpenBlockers     func(ctx context.Context, issue trackers.NormalizedIssue) bool
	AnnotationSuppress  AnnotationLookup
	TrustModePermits    func(binding repos.TrackerBinding) bool
	PatternTrustAboveBy func(issue trackers.NormalizedIssue) bool
}

// Evaluate returns the resulting Eligibility enum value (eligible or
// the appropriate ineligible_* reason). The issueID is used for
// preflight cache lookup. Rules are checked in order; the first
// failing rule short-circuits with that rule's reason code.
func (e *Evaluator) Evaluate(ctx context.Context, issueID uuid.UUID, issue trackers.NormalizedIssue, binding repos.TrackerBinding) (repos.Eligibility, error) {
	// Rule 1: state in {open}.
	if issue.State != trackers.StateOpen {
		return repos.EligIneligibleBlocked, nil
	}

	// Rule 2: claim_status = unclaimed.
	// (NormalizedIssue at adapter level doesn't carry claim status;
	// upserted DB rows default to 'unclaimed'. Treated as a no-op at
	// this layer — the DB defaults handle it.)

	// Rule 3: labels not in IneligibleLabels.
	labels := e.Config.IneligibleLabels
	if len(labels) == 0 {
		labels = DefaultEligibilityConfig().IneligibleLabels
	}
	for _, l := range issue.Labels {
		if containsFold(labels, l) {
			return repos.EligIneligibleLabel, nil
		}
	}

	// Rule 4: paths not in IneligiblePaths.
	paths := e.Config.IneligiblePaths
	if len(paths) == 0 {
		paths = DefaultEligibilityConfig().IneligiblePaths
	}
	for _, p := range paths {
		if p != "" && strings.Contains(strings.ToLower(issue.Description), strings.ToLower(p)) {
			return repos.EligIneligiblePath, nil
		}
	}

	// Rule 5: branch not in IneligibleBranches. v1 issue bodies
	// rarely declare a target branch; check if any ineligible
	// branch name appears in the description (best-effort).
	for _, b := range e.Config.IneligibleBranches {
		if b != "" && strings.Contains(strings.ToLower(issue.Description), strings.ToLower(b)) {
			return repos.EligIneligibleBranch, nil
		}
	}

	// Rule 6: no open blockers.
	if e.HasOpenBlockers != nil && e.HasOpenBlockers(ctx, issue) {
		return repos.EligIneligibleBlocked, nil
	}

	// Rule 7: trust mode permits action. v1 default: permit. E5 wires
	// the real trust-mode check; the hook gives an early seam.
	if e.TrustModePermits != nil && !e.TrustModePermits(binding) {
		return repos.EligIneligibleTrust, nil
	}

	// Rule 10 (before 8 to avoid LLM cost on a suppressed site):
	// // orion:ignore suppresses the issue's referenced callsite.
	if e.AnnotationSuppress != nil && e.AnnotationSuppress(ctx, issue) {
		return repos.EligIneligibleSuppress, nil
	}

	// Rule 9: per-pattern trust score above threshold for orion-filed
	// issues. v1 default: permit (E9 wires the real score). The hook
	// can early-skip rule 8 when the pattern is below threshold —
	// no need to ask the LLM for a pattern we already know is
	// auto-suppressed.
	if e.PatternTrustAboveBy != nil && issue.OrionFiled && !e.PatternTrustAboveBy(issue) {
		return repos.EligIneligiblePattern, nil
	}

	// Rule 8: LLM pre-flight. Skipped if no assessor is wired —
	// caller is responsible for treating that as "permit" since the
	// other 9 rules already passed.
	if e.Preflight != nil {
		res, err := e.Preflight.Assess(ctx, issueID, issue.Title, issue.Description)
		if err != nil {
			return "", err
		}
		if res.Decision == repos.PreflightOutOfScope {
			return repos.EligIneligibleBlocked, nil
		}
	}

	return repos.EligEligible, nil
}

// containsFold returns true if any element of haystack equals needle
// case-insensitively.
func containsFold(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.EqualFold(h, needle) {
			return true
		}
	}
	return false
}
