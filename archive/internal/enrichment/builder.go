package enrichment

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/polaris"
)

// PolarisReader is the subset of polaris.Client the builder uses. An
// interface (vs *polaris.Client) keeps Build mockable in unit tests.
type PolarisReader interface {
	ListKnowledgeInsights(ctx context.Context, opts polaris.KnowledgeInsightsOptions) ([]polaris.KnowledgeInsight, error)
	Search(ctx context.Context, opts polaris.SearchOptions) ([]polaris.SearchHit, error)
	Foresight(ctx context.Context, opts polaris.ForesightOptions) ([]polaris.ForesightChain, error)
	ListApplicableRisks(ctx context.Context, opts polaris.ListApplicableRisksOptions) ([]polaris.ApplicableRisk, error)
}

// Builder constructs IssueContextBlocks from a Polaris reader and a
// run-snapshotted ControlsCatalog.
type Builder struct {
	reader  PolarisReader
	catalog *polaris.ControlsCatalog
}

// NewBuilder returns a Builder. The catalog is the run-snapshotted
// controls (per SPEC §14.6); pass nil only in tests that don't need
// control filtering.
func NewBuilder(reader PolarisReader, catalog *polaris.ControlsCatalog) *Builder {
	return &Builder{reader: reader, catalog: catalog}
}

// Build assembles an IssueContextBlock for one query. Side-effect
// free except for the four Polaris reads it triggers; failures from
// any single read return ErrPolarisFetchFailed (wrapping the cause)
// EXCEPT search and foresight failures, which degrade gracefully
// (Polaris instances may not have those endpoints populated for the
// fixture).
func (b *Builder) Build(ctx context.Context, q Query) (*IssueContextBlock, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}

	out := &IssueContextBlock{
		Query:      q,
		SnapshotAt: time.Now().UTC(),
	}

	// Filter snapshotted controls by pattern + languages. Done here
	// (not in polaris) so the snapshot stays the source of truth.
	if b.catalog != nil {
		out.Controls = filterControls(b.catalog, q)
	}

	// Knowledge insights: required-ish. Failure is fatal because
	// without insights the synthesizer prompt loses its grounding.
	insights, err := b.reader.ListKnowledgeInsights(ctx, polaris.KnowledgeInsightsOptions{
		Tags:        append([]string{}, q.Languages...),
		ControlCode: q.ControlCode,
		Limit:       10,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: insights: %v", ErrPolarisFetchFailed, err)
	}
	out.KnowledgeInsights = insights

	// Search: degrade gracefully on error or empty.
	searchQuery := q.FreeText
	if searchQuery == "" {
		searchQuery = q.Pattern
	}
	if searchQuery != "" {
		hits, serr := b.reader.Search(ctx, polaris.SearchOptions{Query: searchQuery, Limit: 5})
		if serr == nil {
			out.SearchHits = hits
		}
	}

	// Foresight: anchor is the natural-language description of the change.
	anchor := fmt.Sprintf("Add %s to %s in %s", q.Pattern, q.IssueID, q.Service)
	if q.FreeText != "" {
		anchor = q.FreeText
	}
	chains, ferr := b.reader.Foresight(ctx, polaris.ForesightOptions{Anchor: anchor, MaxChains: 3})
	if ferr == nil {
		out.ForesightChains = chains
	}

	// Applicable risks: required. Used to deduplicate in-flight remediation.
	risks, err := b.reader.ListApplicableRisks(ctx, polaris.ListApplicableRisksOptions{
		Service: q.Service,
		Limit:   25,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: risks: %v", ErrPolarisFetchFailed, err)
	}
	out.ApplicableRisks = risks

	return out, nil
}

// filterControls returns the subset of catalog.Controls that match
// the query. v1 is a substring match on Pattern against Name +
// Description; later epics swap this for control_code-driven mapping
// from the gap detector.
func filterControls(catalog *polaris.ControlsCatalog, q Query) []polaris.Control {
	if catalog == nil || q.Pattern == "" {
		return nil
	}
	pat := strings.ToLower(q.Pattern)
	out := make([]polaris.Control, 0, len(catalog.Controls))
	for _, c := range catalog.Controls {
		hay := strings.ToLower(c.Name + " " + c.Description + " " + c.Objective)
		if strings.Contains(hay, pat) {
			out = append(out, c)
		}
	}
	return out
}
