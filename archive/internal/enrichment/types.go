// Package enrichment builds an IssueContextBlock for one detected gap
// (or one tracker issue) per SPEC §13.7. The block snapshots the
// Polaris-side reliability context the patch synthesizer needs:
// applicable controls, knowledge insights, foresight chains, existing
// applicable risks. After construction, the block is read-only;
// downstream consumers (the patch synthesizer, the verifier) MUST NOT
// re-fetch from Polaris during the per-issue loop. This is the SPEC
// §14.6 snapshot discipline made concrete: one fetch per gap, one
// snapshot, no live reads.
package enrichment

import (
	"errors"
	"time"

	"github.com/revelara-ai/orion/internal/polaris"
)

// Sentinel errors.
var (
	// ErrInvalidQuery: required query field missing.
	ErrInvalidQuery = errors.New("enrichment: invalid query")

	// ErrPolarisFetchFailed: a Polaris read returned an error during
	// snapshot construction. Wrapped err carries the underlying cause.
	ErrPolarisFetchFailed = errors.New("enrichment: polaris fetch failed")
)

// Query is the input to Build. It describes the gap (or issue) being
// enriched and the architectural neighborhood the synthesizer cares
// about.
type Query struct {
	// IssueID is the gap ID or tracker external_id. Used for
	// correlation only; not sent to Polaris.
	IssueID string

	// Languages is the set of languages the gap touches (e.g.
	// ["go"]). Drives which knowledge insights are retrieved.
	Languages []string

	// Pattern is the reliability shape (timeout, retry, idempotency).
	// Used as the search seed for knowledge insights.
	Pattern string

	// FreeText is an optional natural-language search query for the
	// /api/search call. Empty falls back to Pattern.
	FreeText string

	// Service is the service name the gap belongs to. Used to scope
	// applicable risks. Empty means all-services.
	Service string

	// ControlCode optionally filters insights to one specific control.
	ControlCode string
}

// Validate checks Query for required fields.
func (q Query) Validate() error {
	if q.IssueID == "" {
		return ErrInvalidQuery
	}
	if q.Pattern == "" && q.FreeText == "" {
		return ErrInvalidQuery
	}
	return nil
}

// IssueContextBlock is the snapshotted enrichment output for one gap.
// Once Build returns, the block is read-only and MUST be passed by
// value (or as an opaque struct) into the synthesizer; no live Polaris
// reads happen between Build and the LLM call.
type IssueContextBlock struct {
	// Query is the input that produced this block. Stored for audit.
	Query Query

	// Controls is the per-gap applicable subset of the run's
	// snapshotted ControlsCatalog (SPEC §14.6). Filtered by
	// the gap's Pattern and the query's languages.
	Controls []polaris.Control

	// KnowledgeInsights are the snapshotted insights from Polaris.
	KnowledgeInsights []polaris.KnowledgeInsight

	// ForesightChains are the snapshotted foresight chains.
	ForesightChains []polaris.ForesightChain

	// SearchHits are the supplementary /api/search results.
	SearchHits []polaris.SearchHit

	// ApplicableRisks are existing in-flight risks for this service.
	ApplicableRisks []polaris.ApplicableRisk

	// SnapshotAt is the wall-clock RFC3339 timestamp at which Build
	// completed. Recorded with each generated patch for provenance.
	SnapshotAt time.Time
}
