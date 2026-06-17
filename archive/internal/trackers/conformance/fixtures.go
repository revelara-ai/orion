package conformance

import (
	"time"

	"github.com/revelara-ai/orion/internal/trackers"
)

// Fixture is the canned state a Factory returns alongside the adapter
// + binding. The suite uses it to assert on FetchCandidates output
// and to source ExternalIDs for FetchByExternalIDs / UpdateState /
// Comment.
type Fixture struct {
	// Issues are the synthetic issues the adapter should return from
	// FetchCandidates when called with since=0.
	Issues []trackers.NormalizedIssue

	// MidpointTime is a timestamp such that some fixture issues have
	// LastUpdated before it and some after. Used by the suite's
	// FetchCandidates_IncrementalSince subtest.
	MidpointTime time.Time
}

// DefaultFixture builds a small in-memory issue set suitable for the
// suite. Three issues: one old, one at midpoint, one fresh.
//
// Returns a fresh slice on every call so tests can mutate without
// cross-contamination.
func DefaultFixture() Fixture {
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	midpoint := now.Add(-30 * time.Minute)
	return Fixture{
		MidpointTime: midpoint,
		Issues: []trackers.NormalizedIssue{
			{
				ExternalID:  "stub:org/repo#1",
				ExternalURL: "https://example.test/stub/org/repo/issues/1",
				Title:       "Old issue, before midpoint",
				Description: "Synthesized for conformance suite",
				State:       trackers.StateOpen,
				Labels:      []string{"bug"},
				LastUpdated: now.Add(-2 * time.Hour),
			},
			{
				ExternalID:  "stub:org/repo#2",
				ExternalURL: "https://example.test/stub/org/repo/issues/2",
				Title:       "Mid issue, at midpoint",
				Description: "Synthesized for conformance suite",
				State:       trackers.StateOpen,
				Labels:      []string{"reliability"},
				LastUpdated: midpoint,
			},
			{
				ExternalID:  "stub:org/repo#3",
				ExternalURL: "https://example.test/stub/org/repo/issues/3",
				Title:       "Fresh issue, after midpoint",
				Description: "Synthesized for conformance suite",
				State:       trackers.StateInProgress,
				Labels:      []string{"reliability", "p1"},
				LastUpdated: now,
			},
		},
	}
}
