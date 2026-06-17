// Package trackers defines the v1 TrackerAdapter contract per SPEC §8.1
// and the NormalizedIssue shape per SPEC §4.1.7.
//
// Adapter implementations live in subpackages (internal/trackers/github,
// internal/trackers/linear). They MUST pass the conformance suite in
// internal/trackers/conformance.
//
// The contract is read-mostly: FetchCandidates polls + FetchByExternalIDs
// re-syncs are the bread-and-butter calls; Create + UpdateState + Comment
// are write paths gated by §6.4 trust mode and §8.7 auto-file caps.
// HealthCheck is the v1-mandatory probe that the ingestion driver
// (E2-6) consults before polling a binding.
package trackers

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Sentinel errors. Callers errors.Is against these to distinguish
// adapter-level failure modes from caller-side validation errors.
var (
	// ErrInvalidBinding: TrackerBinding has malformed config or missing
	// credentials_ref.
	ErrInvalidBinding = errors.New("trackers: invalid binding")

	// ErrUnauthenticated: adapter could not authenticate. Caller MUST
	// surface this as a binding-health failure (don't retry forever).
	ErrUnauthenticated = errors.New("trackers: unauthenticated")

	// ErrRateLimited: provider returned a rate-limit indication. Caller
	// SHOULD back off; the adapter exposes Retry-After via the wrapped
	// error where available.
	ErrRateLimited = errors.New("trackers: rate limited")

	// ErrNotFound: external_id doesn't resolve in the upstream tracker
	// (issue deleted, moved, or never existed).
	ErrNotFound = errors.New("trackers: external id not found")

	// ErrProviderFailure: a non-retryable provider-side error. Wraps the
	// raw upstream error for diagnostics.
	ErrProviderFailure = errors.New("trackers: provider failure")

	// ErrCapabilityUnsupported: caller invoked a method the adapter
	// declares unsupported via Capabilities().
	ErrCapabilityUnsupported = errors.New("trackers: capability unsupported")
)

// TrackerKind enumerates v1 providers. Wire-stable; new providers add
// values at the end of the list.
type TrackerKind string

// v1 providers per SPEC §8.2.
const (
	TrackerKindGitHubIssues TrackerKind = "github_issues"
	TrackerKindLinear       TrackerKind = "linear"
)

// NormalizedState is the canonical state enum every adapter normalizes
// upstream states into (SPEC §4.1.7).
type NormalizedState string

// Canonical states.
const (
	StateOpen       NormalizedState = "open"
	StateInProgress NormalizedState = "in_progress"
	StateBlocked    NormalizedState = "blocked"
	StateClosed     NormalizedState = "closed"
	StateCancelled  NormalizedState = "cancelled"
)

// NormalizedIssue is the orion-side canonical issue shape. Field set
// mirrors SPEC §4.1.7; durable persistence lives in the
// internal/repos package (added in E2-5). v1 keeps the type here so
// adapter code can construct/return it without an upward dependency
// on the repos package.
type NormalizedIssue struct {
	// ID is Orion's internal identifier. Populated by the persistence
	// layer on upsert; adapters return rows with ID == uuid.Nil.
	ID uuid.UUID `json:"id,omitempty"`

	// OrgID, RepoID, TrackerBindingID are provenance. Populated by the
	// ingestion driver (E2-6) from binding context; adapters MAY leave
	// them zero.
	OrgID            uuid.UUID `json:"org_id,omitempty"`
	RepoID           uuid.UUID `json:"repo_id,omitempty"`
	TrackerBindingID uuid.UUID `json:"tracker_binding_id,omitempty"`

	// ExternalID is the SPEC §4.2 stable identifier:
	// <provider>:<scope>#<id>.
	ExternalID string `json:"external_id"`

	// ExternalURL is the direct browser link in the upstream tracker.
	ExternalURL string `json:"external_url"`

	// Title and Description carry the issue body verbatim. The pre-flight
	// eligibility step (E2-8) parses these.
	Title       string `json:"title"`
	Description string `json:"description"`

	// Priority is the tracker-native priority normalized to a 0-4 scale
	// (0 = critical). Nil when the source tracker doesn't expose a
	// priority field.
	Priority *int `json:"priority,omitempty"`

	// State is the normalized state enum.
	State NormalizedState `json:"state"`

	// Labels are lower-cased, deduplicated.
	Labels []string `json:"labels,omitempty"`

	// PolarisRiskID links to the Polaris-tracked risk when known. Set
	// for Orion-filed risk issues (§8.7) and for tracker issues whose
	// body references a Polaris risk URL.
	PolarisRiskID *uuid.UUID `json:"polaris_risk_id,omitempty"`

	// OrionFiled is true when Orion created this issue (§8.7 auto-file).
	OrionFiled bool `json:"orion_filed"`

	// LastUpdated is the upstream tracker's last-modified timestamp.
	// Used by the ingestion driver to compute `since` for incremental
	// polls.
	LastUpdated time.Time `json:"last_updated"`
}

// IssueDraft is the input to Adapter.Create. v1 supports the minimal
// surface needed by §8.7 auto-file: title + body + labels.
type IssueDraft struct {
	// Title is the issue title. Required.
	Title string

	// Body is the issue body (markdown for GitHub, plain text or rich
	// content for Linear).
	Body string

	// Labels to apply on creation. Adapters MAY add provider-default
	// labels (e.g. "orion-filed" per §8.7) on top of these.
	Labels []string
}

// TrackerCapabilities advertises what an adapter supports. Callers
// gate optional operations (Create, UpdateState, Comment) by checking
// the corresponding bool.
type TrackerCapabilities struct {
	// CanCreate is true when the adapter has a working POST/mutation
	// for issue creation. Required for §8.7 auto-file participation.
	CanCreate bool

	// CanUpdateState is true when the adapter can transition issue
	// state (open → closed, etc.). Required for E6 reconciliation.
	CanUpdateState bool

	// CanComment is true when the adapter can post comments. Required
	// for E6 PR-link write-back.
	CanComment bool

	// SupportsLabelFilter is true when FetchCandidates honors
	// binding.config.label_filter to scope the query upstream.
	SupportsLabelFilter bool

	// SupportsSince is true when FetchCandidates honors the `since`
	// parameter at the provider level (vs. fetching everything and
	// filtering client-side).
	SupportsSince bool
}

// TrackerBinding is the v1 in-memory shape callers pass to adapter
// methods. The persistent storage shape (with credentials_ref, etc.)
// lives in internal/repos (E2-1); adapters never read the encrypted
// credential blob directly — they receive the resolved Credentials
// here.
type TrackerBinding struct {
	// ID is the binding's persistent identifier (matches DB row).
	ID uuid.UUID

	// OrgID for RLS context.
	OrgID uuid.UUID

	// RepoID identifies the ConnectedRepo this binding belongs to.
	RepoID uuid.UUID

	// Kind selects the adapter.
	Kind TrackerKind

	// Config is the adapter-specific JSONB blob from
	// SPEC §4.1.2 (Linear project slug, GitHub label filter, etc.).
	Config map[string]any

	// Credentials carries the resolved auth material the adapter needs.
	// Caller (binding repository) decrypts before passing.
	Credentials Credentials
}

// Credentials is the resolved auth payload for one binding. v1 covers
// the auth shapes the two adapters use:
//
//   - GitHub Issues: AppToken (an installation token, minted by
//     internal/github from the App's JWT)
//   - Linear: OAuth2AccessToken + OAuth2RefreshToken + ExpiresAt
//     (rotated per polaris's TokenRefresher pattern)
//
// Adapter implementations type-assert / inspect the populated fields
// to determine which auth shape this binding uses.
type Credentials struct {
	// AppToken is a single bearer token (GitHub App installation token,
	// Personal Access Token, etc.). Set for non-OAuth providers.
	AppToken string

	// OAuth2AccessToken + OAuth2RefreshToken + ExpiresAt are the
	// rotating-OAuth2 fields. Set for Linear and (future) Notion.
	OAuth2AccessToken  string
	OAuth2RefreshToken string
	ExpiresAt          time.Time

	// Extra carries provider-specific resolved fields (Linear workspace
	// ID, GitHub installation_id, etc.).
	Extra map[string]string
}

// TrackerAdapter is the v1 contract every provider implements. SPEC
// §8.1 defines the surface; implementations MUST pass the conformance
// suite in internal/trackers/conformance.
type TrackerAdapter interface {
	// Kind returns the wire-stable provider name.
	Kind() TrackerKind

	// FetchCandidates returns issues updated since `since`. The adapter
	// MAY return more than what was strictly updated (e.g. when the
	// upstream API doesn't honor a precise since cursor); the caller
	// dedupes by external_id and last_updated.
	FetchCandidates(ctx context.Context, binding TrackerBinding, since time.Time) ([]NormalizedIssue, error)

	// FetchByExternalIDs re-syncs specific issues by id. Used by the
	// reconciler (E6) to refresh state after PR events.
	FetchByExternalIDs(ctx context.Context, binding TrackerBinding, ids []string) ([]NormalizedIssue, error)

	// Create files a new issue. Gated by binding.auto_file + trust mode
	// at the caller (§8.7). Returns the freshly-created issue with its
	// upstream-assigned external_id populated.
	Create(ctx context.Context, binding TrackerBinding, draft IssueDraft) (NormalizedIssue, error)

	// UpdateState transitions the issue to the given normalized state.
	// Adapters MAY translate StateBlocked → labels/flags when the
	// upstream tracker has no native blocked state.
	UpdateState(ctx context.Context, binding TrackerBinding, externalID string, state NormalizedState) error

	// Comment posts a comment on the issue.
	Comment(ctx context.Context, binding TrackerBinding, externalID, body string) error

	// Capabilities returns the adapter's feature flags. Callers MUST
	// consult this before invoking optional operations.
	Capabilities() TrackerCapabilities

	// HealthCheck probes the adapter against the binding's
	// credentials. Returns nil on success; any error MUST cause the
	// caller to skip this binding on the current ingestion tick.
	HealthCheck(ctx context.Context, binding TrackerBinding) error
}
