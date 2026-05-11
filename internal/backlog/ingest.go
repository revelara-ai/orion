// Package backlog implements the backlog ingestion driver from
// SPEC §8.3 / Epic 2. It fans across all enabled TrackerBindings,
// invokes adapter.FetchCandidates(since=last_synced_at), normalizes
// the results, and upserts into NormalizedIssue.
//
// v1 exposes the driver via cmd/orion-cli backlog ingest. E4
// Conductor will wrap it with a scheduled tick + on-demand trigger.
package backlog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/database"
	"github.com/revelara-ai/orion/internal/repos"
	"github.com/revelara-ai/orion/internal/trackers"
)

// AdapterFactory mints a TrackerAdapter for a kind. Production wires
// trackers.NewByKind; tests inject stubs.
type AdapterFactory func(kind trackers.TrackerKind) (trackers.TrackerAdapter, error)

// CredentialsResolver turns a stored TrackerBinding (with config +
// credentials_ref) into the resolved Credentials the adapter consumes.
// Production wires the encrypted_oauth_credential repo for OAuth
// providers; GitHub's installation-token path doesn't need a
// credential row (it's derived from binding.Config's app_id +
// installation_id + private_key_pem).
type CredentialsResolver func(ctx context.Context, binding repos.TrackerBinding) (trackers.Credentials, error)

// Driver is the backlog ingestion fan-out. One Driver instance is
// safe for concurrent calls across distinct bindings.
type Driver struct {
	// Bindings/Repos/Issues are the persistence layer; all writes
	// are tenant-scoped via the underlying *db.RLSPool.
	Bindings *repos.TrackerBindingRepo
	Repos    *repos.ConnectedRepoRepo
	Issues   *repos.NormalizedIssueRepo

	// AdapterFactory mints adapters keyed on TrackerKind. Required.
	AdapterFactory AdapterFactory

	// ResolveCredentials resolves the binding's stored credentials
	// reference to the in-memory Credentials shape the adapter
	// consumes. Required.
	ResolveCredentials CredentialsResolver
}

// Result is the per-binding ingest summary returned by IngestBinding.
type Result struct {
	BindingID      uuid.UUID
	IssuesFetched  int
	IssuesUpserted int
	Since          time.Time
	Errors         []string
}

// IngestBinding runs one tick for one binding. Steps:
//
//  1. Load the TrackerBinding + its ConnectedRepo
//  2. Resolve the binding's Credentials
//  3. Mint the adapter (by Kind)
//  4. HealthCheck — fail-fast, return error without iterating
//  5. Compute since = max(last_synced_at) for already-ingested issues
//     in this binding (zero if no prior rows)
//  6. FetchCandidates(since)
//  7. For each candidate, upsert into NormalizedIssue (org_id and
//     foreign keys derived from binding state, not adapter response)
//
// Returns a Result with counts + errors. Per-issue upsert errors are
// collected in Result.Errors so a single bad row doesn't abort the
// whole binding.
func (d *Driver) IngestBinding(ctx context.Context, bindingID uuid.UUID) (Result, error) {
	if err := d.validate(); err != nil {
		return Result{}, err
	}
	out := Result{BindingID: bindingID}

	stored, err := d.Bindings.Get(ctx, bindingID)
	if err != nil {
		return out, fmt.Errorf("backlog: get binding: %w", err)
	}

	creds, err := d.ResolveCredentials(ctx, *stored)
	if err != nil {
		return out, fmt.Errorf("backlog: resolve credentials: %w", err)
	}

	adapter, err := d.AdapterFactory(trackers.TrackerKind(stored.Kind))
	if err != nil {
		return out, fmt.Errorf("backlog: build adapter: %w", err)
	}

	adapterBinding := trackers.TrackerBinding{
		ID:          stored.ID,
		OrgID:       stored.OrgID,
		RepoID:      stored.RepoID,
		Kind:        trackers.TrackerKind(stored.Kind),
		Config:      stored.Config,
		Credentials: creds,
	}

	if err := adapter.HealthCheck(ctx, adapterBinding); err != nil {
		return out, fmt.Errorf("backlog: health check: %w", err)
	}

	since, err := d.Issues.MaxLastSyncedAt(ctx, bindingID)
	if err != nil {
		return out, fmt.Errorf("backlog: max last_synced_at: %w", err)
	}
	out.Since = since

	fetched, err := adapter.FetchCandidates(ctx, adapterBinding, since)
	if err != nil {
		return out, fmt.Errorf("backlog: fetch candidates: %w", err)
	}
	out.IssuesFetched = len(fetched)

	now := time.Now().UTC()
	for _, issue := range fetched {
		row := repos.NormalizedIssue{
			OrgID:            stored.OrgID,
			RepoID:           stored.RepoID,
			TrackerBindingID: stored.ID,
			ExternalID:       issue.ExternalID,
			ExternalURL:      issue.ExternalURL,
			Title:            issue.Title,
			Description:      issue.Description,
			Priority:         priorityToInt16(issue.Priority),
			State:            normalizeStateForRepo(issue.State),
			Labels:           issue.Labels,
			LastSyncedAt:     now,
		}
		if _, err := d.Issues.Upsert(ctx, row); err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("%s: %v", issue.ExternalID, err))
			continue
		}
		out.IssuesUpserted++
	}
	return out, nil
}

// validate verifies the driver's required fields are wired before
// running.
func (d *Driver) validate() error {
	if d == nil {
		return errors.New("backlog: nil driver")
	}
	if d.Bindings == nil || d.Repos == nil || d.Issues == nil {
		return errors.New("backlog: repos not wired")
	}
	if d.AdapterFactory == nil {
		return errors.New("backlog: AdapterFactory required")
	}
	if d.ResolveCredentials == nil {
		return errors.New("backlog: ResolveCredentials required")
	}
	return nil
}

// priorityToInt16 converts the adapter-side *int priority to the
// repo-side *int16. nil round-trips as nil. Adapter priorities are
// already constrained to 0-4 by the contract; we just narrow the
// int width.
func priorityToInt16(p *int) *int16 {
	if p == nil {
		return nil
	}
	// Clamp to int16 range so an out-of-range adapter return doesn't
	// surface as an opaque overflow; v1 priorities are always 0-4
	// per the contract, so the clamp is a defensive belt.
	val := *p
	if val < -32768 {
		val = -32768
	}
	if val > 32767 {
		val = 32767
	}
	v := int16(val) //#nosec G115 -- val is clamped to int16 range above
	return &v
}

// normalizeStateForRepo bridges the adapter-side enum
// (trackers.NormalizedState) and the repo-side enum
// (repos.NormalizedState). They share string values; this is just a
// type cast wrapped for clarity.
func normalizeStateForRepo(s trackers.NormalizedState) repos.NormalizedState {
	switch s {
	case trackers.StateOpen:
		return repos.StateOpen
	case trackers.StateInProgress:
		return repos.StateInProgress
	case trackers.StateBlocked:
		return repos.StateBlocked
	case trackers.StateClosed:
		return repos.StateClosed
	case trackers.StateCancelled:
		return repos.StateCancelled
	}
	return repos.StateOpen
}

// Compile-time: avoid unused-import warning when database is only
// referenced via the type-checked CredentialsResolver signature.
var _ database.RLSPool
