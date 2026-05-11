// Package repos holds typed repositories for Orion's Postgres
// entities. Per-tenant repositories all take *database.RLSPool so
// RLS context propagates from ctx; system-level repositories (none
// yet in v1) would take the raw *database.Pool and SET LOCAL ROLE
// inside their tx.
package repos

import (
	"time"

	"github.com/google/uuid"
)

// ConnectedRepo mirrors SPEC §4.1.1.
type ConnectedRepo struct {
	ID            uuid.UUID
	OrgID         uuid.UUID
	Provider      string // 'github' in v1
	AppInstallID  string
	RepoFullName  string
	DefaultBranch string
	ServicePath   *string
	Enabled       bool
	TrustMode     TrustMode
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// TrustMode mirrors SPEC §6.4 trust ladder. Stored as text in the
// DB; the enum is enforced by the table's CHECK constraint AND by
// the Go type so callers can't construct an invalid value.
type TrustMode string

// Trust ladder rungs.
const (
	TrustShadow  TrustMode = "shadow"
	TrustDraft   TrustMode = "draft"
	TrustStaging TrustMode = "staging"
	TrustFull    TrustMode = "full"
)

// TrackerBinding mirrors SPEC §4.1.2.
type TrackerBinding struct {
	ID             uuid.UUID
	OrgID          uuid.UUID
	RepoID         uuid.UUID
	Kind           TrackerKind
	Config         map[string]any
	CredentialsRef string
	Enabled        bool
	AutoFile       bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// TrackerKind mirrors the enum from internal/trackers but is
// duplicated here so this package has no upward dep on
// internal/trackers (avoiding cyclic imports if trackers ever wants
// to depend on repos).
type TrackerKind string

// v1 trackers.
const (
	TrackerGitHubIssues TrackerKind = "github_issues"
	TrackerLinear       TrackerKind = "linear"
)
