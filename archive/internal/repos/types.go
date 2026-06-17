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

// NormalizedIssue mirrors SPEC §4.1.7 at the DB layer. Duplicated
// from internal/trackers.NormalizedIssue to avoid the cyclic dep
// (trackers' types depend on repos for nothing; repos can't depend
// on trackers because trackers' adapters would then depend on us
// transitively). The two structs converge field-for-field; the
// ingestion driver (E2-6) does the conversion.
type NormalizedIssue struct {
	ID               uuid.UUID
	OrgID            uuid.UUID
	RepoID           uuid.UUID
	TrackerBindingID uuid.UUID
	ExternalID       string
	ExternalURL      string
	Title            string
	Description      string
	Priority         *int16
	State            NormalizedState
	Labels           []string
	PolarisRiskID    *uuid.UUID
	OrionFiled       bool
	ClaimStatus      ClaimStatus
	Eligibility      *Eligibility
	DedupSignature   *string
	LastSyncedAt     time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// NormalizedState mirrors the canonical state enum from trackers.
type NormalizedState string

// Canonical states.
const (
	StateOpen       NormalizedState = "open"
	StateInProgress NormalizedState = "in_progress"
	StateBlocked    NormalizedState = "blocked"
	StateClosed     NormalizedState = "closed"
	StateCancelled  NormalizedState = "cancelled"
)

// ClaimStatus is the v1 claim-state machine column. The full
// transition logic (claim, release, supersede) lives in Epic 4
// (Conductor); v1 ships only the column + default + literal
// updaters.
type ClaimStatus string

// Claim states.
const (
	ClaimUnclaimed ClaimStatus = "unclaimed"
	ClaimClaimed   ClaimStatus = "claimed"
	ClaimInSession ClaimStatus = "in_session"
	ClaimReleased  ClaimStatus = "released"
)

// Eligibility enumerates SPEC §8.4 outcomes.
type Eligibility string

// Eligibility values.
const (
	EligEligible           Eligibility = "eligible"
	EligIneligiblePattern  Eligibility = "ineligible_pattern"
	EligIneligiblePath     Eligibility = "ineligible_path"
	EligIneligibleLabel    Eligibility = "ineligible_label"
	EligIneligibleBranch   Eligibility = "ineligible_branch"
	EligIneligibleBlocked  Eligibility = "ineligible_blocked"
	EligIneligibleSuppress Eligibility = "ineligible_suppressed"
	EligIneligibleTrust    Eligibility = "ineligible_trust_mode"
)

// DetectionRunMode enumerates SPEC §15.2 phase modes.
type DetectionRunMode string

// Run modes.
const (
	DetectionModeFull        DetectionRunMode = "full"
	DetectionModeIncremental DetectionRunMode = "incremental"
	DetectionModePostMerge   DetectionRunMode = "post_merge"
)

// DetectionPhase is the lifecycle state of a run row. Quiescent is a
// SUCCESS state per SPEC §15.3.1 (empty-backlog cheap path), distinct
// from completed so the customer-facing surface can frame it positively.
type DetectionPhase string

// Phases.
const (
	DetectionPhaseRunning   DetectionPhase = "running"
	DetectionPhaseCompleted DetectionPhase = "completed"
	DetectionPhaseQuiescent DetectionPhase = "quiescent"
	DetectionPhaseFailed    DetectionPhase = "failed"
)

// DetectionRun mirrors the detection_runs row per SPEC §15.2 phase 7
// and §15.4 (self-referential-loop provenance counters).
type DetectionRun struct {
	ID                     uuid.UUID
	OrgID                  uuid.UUID
	BindingID              uuid.UUID
	Mode                   DetectionRunMode
	Phase                  DetectionPhase
	Quiescent              bool
	FindingsTotal          int
	FindingsNew            int
	FindingsDeduped        int
	FindingsSuppressed     int
	OrionFiledProcessed    int
	CustomerFiledProcessed int
	PolarisPriorProcessed  int
	StartedAt              time.Time
	FinishedAt             *time.Time
	ErrorMessage           *string
	SelfReferentialWarning bool
}

// DetectionFinding is one row of the detection_findings ledger. Per
// SPEC §15.2 phase 7 the ledger retains suppressed and deduped findings
// alongside new ones so customers can audit Orion's decisions; the
// suppressed/deduped flags differentiate them at the row level.
type DetectionFinding struct {
	ID             uuid.UUID
	OrgID          uuid.UUID
	RunID          uuid.UUID
	Slug           string
	Title          string
	Category       string
	Confidence     string
	Severity       string
	ControlCodes   []string
	FilePath       string
	LineNo         int
	Fingerprint    string
	DedupSignature *string
	Suppressed     bool
	Deduped        bool
	CreatedAt      time.Time
}
