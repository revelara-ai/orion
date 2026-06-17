// Package escalation provides the deterministic escalation router described in §14.8 of the Orion spec.
package escalation

import "fmt"

type FailureKind string

const (
	HarnessFailure     FailureKind = "harness_failure"
	PlatformCritical   FailureKind = "platform_critical"
	IntegrationBreak   FailureKind = "integration_break"
	CustomerPatch      FailureKind = "customer_patch"
	CustomerEligible   FailureKind = "customer_eligible"
	CustomerQuarantine FailureKind = "customer_quarantine"
)

// FailEvent describes a failure discovered in an Orion Agent Loop run.
type FailEvent struct {
	Kind        FailureKind
	RunID       string
	Description string
}

// Validate checks that f describes a parseable, non-empty event.
func (f *FailEvent) Validate() error {
	if f.Kind == "" {
		return fmt.Errorf("escalation.Validate: Kind must be non-empty")
	}
	switch f.Kind {
	case HarnessFailure, PlatformCritical, IntegrationBreak,
		CustomerPatch, CustomerEligible, CustomerQuarantine:
	default:
		return fmt.Errorf("escalation.Validate: unknown failure kind %q", f.Kind)
	}
	if f.RunID == "" {
		return fmt.Errorf("escalation.Validate: RunID must be non-empty")
	}
	return nil
}

// Route maps a FailEvent to an escalation recipient string (§14.8).
func (f *FailEvent) Route() string {
	switch f.Kind {
	case HarnessFailure:
		return "revelara:harness_failure"
	case PlatformCritical, CustomerQuarantine:
		return "revelara:platform_critical"
	case IntegrationBreak:
		return "revelara:integration_break"
	case CustomerPatch:
		return "customer:patch_review"
	case CustomerEligible:
		return "customer:eligibility_question"
	default:
		return ""
	}
}

// Route maps a FailureKind constant directly to an escalation recipient string (§14.8).
func (k FailureKind) Route() string {
	return (&FailEvent{Kind: k}).Route()
}
