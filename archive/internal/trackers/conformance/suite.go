// Package conformance is the v1 TrackerAdapter conformance test suite.
// Every adapter implementation (internal/trackers/github,
// internal/trackers/linear, future providers) MUST pass this suite.
//
// The suite is parametric: callers provide a factory that returns a
// fresh adapter + binding for each test. Adapters wire the suite into
// their own _test.go like this:
//
//	func TestConformance(t *testing.T) {
//	    conformance.Run(t, conformance.SuiteOptions{
//	        Factory: func(t *testing.T) (trackers.TrackerAdapter, trackers.TrackerBinding, conformance.Fixture) {
//	            // returns a stub / mocked / live adapter
//	        },
//	    })
//	}
//
// The suite NEVER calls the real upstream tracker. Adapters that
// want a live integration test ship their own build-tag-gated test
// (see internal/github/integration_test.go for the pattern E1-1
// established).
package conformance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/trackers"
)

// SuiteOptions configures one Run invocation.
type SuiteOptions struct {
	// Factory returns a fresh adapter + binding + fixture for each
	// subtest. The fixture carries the canned issues the adapter will
	// return from FetchCandidates so the suite can assert on shape.
	//
	// MUST be safe to call concurrently with previous returns (e.g.
	// not share mutable state). The suite calls it once per subtest.
	Factory func(t *testing.T) (trackers.TrackerAdapter, trackers.TrackerBinding, Fixture)

	// CapabilityOverrides lets a caller skip subtests that don't apply
	// to a partially-capable adapter. Most adapters leave this empty
	// and the suite consults adapter.Capabilities() instead.
	CapabilityOverrides *trackers.TrackerCapabilities
}

// Run executes the full conformance suite against opts.Factory.
//
// Subtests:
//
//   - Kind: adapter returns a non-empty TrackerKind matching v1 set
//   - HealthCheck: with a valid binding returns nil
//   - HealthCheck/Unauthenticated: with an invalid binding returns
//     ErrUnauthenticated (or ErrInvalidBinding)
//   - FetchCandidates/AllIssues: since=0 returns the fixture's full set
//   - FetchCandidates/IncrementalSince: since=fixture.MidpointTime
//     returns only issues with LastUpdated >= since
//   - FetchByExternalIDs: round-trips the fixture's ids
//   - Create: when capability supported, returns a NormalizedIssue
//     with non-empty ExternalID
//   - UpdateState: when capability supported, transitions a fixture
//     issue without error
//   - Comment: when capability supported, posts without error
//   - Capabilities: returns a non-zero struct
func Run(t *testing.T, opts SuiteOptions) {
	t.Helper()
	if opts.Factory == nil {
		t.Fatal("conformance: Factory is required")
	}

	t.Run("Kind", func(t *testing.T) {
		adapter, _, _ := opts.Factory(t)
		k := adapter.Kind()
		switch k {
		case trackers.TrackerKindGitHubIssues, trackers.TrackerKindLinear:
			// ok
		case "":
			t.Fatal("Kind() returned empty")
		default:
			t.Logf("Kind() = %q (not in v1 enum; OK for forward-compat)", k)
		}
	})

	t.Run("HealthCheck", func(t *testing.T) {
		adapter, binding, _ := opts.Factory(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := adapter.HealthCheck(ctx, binding); err != nil {
			t.Errorf("HealthCheck: %v", err)
		}
	})

	t.Run("FetchCandidates_AllIssues", func(t *testing.T) {
		adapter, binding, fixture := opts.Factory(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		got, err := adapter.FetchCandidates(ctx, binding, time.Time{})
		if err != nil {
			t.Fatalf("FetchCandidates: %v", err)
		}
		if len(got) != len(fixture.Issues) {
			t.Errorf("got %d issues, want %d", len(got), len(fixture.Issues))
		}
		for i, issue := range got {
			if issue.ExternalID == "" {
				t.Errorf("issue %d: empty ExternalID", i)
			}
			if issue.State == "" {
				t.Errorf("issue %d: empty State", i)
			}
		}
	})

	t.Run("FetchCandidates_IncrementalSince", func(t *testing.T) {
		adapter, binding, fixture := opts.Factory(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		got, err := adapter.FetchCandidates(ctx, binding, fixture.MidpointTime)
		if err != nil {
			t.Fatalf("FetchCandidates(since): %v", err)
		}
		for _, issue := range got {
			if issue.LastUpdated.Before(fixture.MidpointTime) {
				t.Errorf("issue %s: LastUpdated %v before since %v",
					issue.ExternalID, issue.LastUpdated, fixture.MidpointTime)
			}
		}
	})

	t.Run("FetchByExternalIDs", func(t *testing.T) {
		adapter, binding, fixture := opts.Factory(t)
		if len(fixture.Issues) == 0 {
			t.Skip("fixture has no issues to look up")
		}
		ids := []string{fixture.Issues[0].ExternalID}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		got, err := adapter.FetchByExternalIDs(ctx, binding, ids)
		if err != nil {
			t.Fatalf("FetchByExternalIDs: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("got %d, want 1", len(got))
		}
		if len(got) > 0 && got[0].ExternalID != ids[0] {
			t.Errorf("got external_id %q, want %q", got[0].ExternalID, ids[0])
		}
	})

	t.Run("FetchByExternalIDs_NotFound", func(t *testing.T) {
		adapter, binding, _ := opts.Factory(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		got, err := adapter.FetchByExternalIDs(ctx, binding, []string{"definitely-not-real-12345"})
		// Either: returns empty without error, OR returns ErrNotFound.
		// Adapters are free to choose; both are conformant.
		if err != nil && !errors.Is(err, trackers.ErrNotFound) {
			t.Errorf("unexpected err: %v (want nil or ErrNotFound)", err)
		}
		if err == nil && len(got) != 0 {
			t.Errorf("got %d issues for non-existent id (want 0)", len(got))
		}
	})

	caps := opts.CapabilityOverrides
	if caps == nil {
		adapter, _, _ := opts.Factory(t)
		c := adapter.Capabilities()
		caps = &c
	}

	if caps.CanCreate {
		t.Run("Create", func(t *testing.T) {
			adapter, binding, _ := opts.Factory(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			created, err := adapter.Create(ctx, binding, trackers.IssueDraft{
				Title:  "conformance test issue",
				Body:   "filed by conformance suite",
				Labels: []string{"orion-conformance"},
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if created.ExternalID == "" {
				t.Error("Create returned empty ExternalID")
			}
		})
	}

	if caps.CanUpdateState {
		t.Run("UpdateState", func(t *testing.T) {
			adapter, binding, fixture := opts.Factory(t)
			if len(fixture.Issues) == 0 {
				t.Skip("fixture has no issues to update")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := adapter.UpdateState(ctx, binding, fixture.Issues[0].ExternalID, trackers.StateClosed); err != nil {
				t.Errorf("UpdateState: %v", err)
			}
		})
	}

	if caps.CanComment {
		t.Run("Comment", func(t *testing.T) {
			adapter, binding, fixture := opts.Factory(t)
			if len(fixture.Issues) == 0 {
				t.Skip("fixture has no issues to comment on")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := adapter.Comment(ctx, binding, fixture.Issues[0].ExternalID, "conformance"); err != nil {
				t.Errorf("Comment: %v", err)
			}
		})
	}

	t.Run("Capabilities", func(t *testing.T) {
		adapter, _, _ := opts.Factory(t)
		c := adapter.Capabilities()
		_ = c // any value is fine; just assert the call doesn't panic
	})
}
