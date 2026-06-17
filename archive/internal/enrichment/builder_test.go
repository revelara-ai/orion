package enrichment

import (
	"context"
	"errors"
	"testing"

	"github.com/revelara-ai/orion/internal/polaris"
)

// stubReader implements PolarisReader with canned responses.
type stubReader struct {
	insights []polaris.KnowledgeInsight
	hits     []polaris.SearchHit
	chains   []polaris.ForesightChain
	risks    []polaris.ApplicableRisk

	insightsErr error
	hitsErr     error
	chainsErr   error
	risksErr    error

	insightsCalls int
	hitsCalls     int
	chainsCalls   int
	risksCalls    int
}

func (s *stubReader) ListKnowledgeInsights(ctx context.Context, opts polaris.KnowledgeInsightsOptions) ([]polaris.KnowledgeInsight, error) {
	s.insightsCalls++
	return s.insights, s.insightsErr
}
func (s *stubReader) Search(ctx context.Context, opts polaris.SearchOptions) ([]polaris.SearchHit, error) {
	s.hitsCalls++
	return s.hits, s.hitsErr
}
func (s *stubReader) Foresight(ctx context.Context, opts polaris.ForesightOptions) ([]polaris.ForesightChain, error) {
	s.chainsCalls++
	return s.chains, s.chainsErr
}
func (s *stubReader) ListApplicableRisks(ctx context.Context, opts polaris.ListApplicableRisksOptions) ([]polaris.ApplicableRisk, error) {
	s.risksCalls++
	return s.risks, s.risksErr
}

func TestBuildHappyPath(t *testing.T) {
	reader := &stubReader{
		insights: []polaris.KnowledgeInsight{{Title: "Use timeouts"}},
		hits:     []polaris.SearchHit{{Title: "incident-123"}},
		chains:   []polaris.ForesightChain{{Steps: []string{"a", "b"}}},
		risks:    []polaris.ApplicableRisk{{ID: "R-1"}},
	}
	catalog := &polaris.ControlsCatalog{Controls: []polaris.Control{
		{ControlCode: "RC-T-1", Name: "Outbound timeout"},
		{ControlCode: "RC-R-1", Name: "Retry hygiene"},
	}}
	b := NewBuilder(reader, catalog)
	q := Query{IssueID: "g1", Pattern: "timeout", Service: "frontend", Languages: []string{"go"}}
	block, err := b.Build(context.Background(), q)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if block.Query.IssueID != "g1" {
		t.Errorf("Query.IssueID = %q", block.Query.IssueID)
	}
	if len(block.Controls) != 1 {
		t.Errorf("filtered controls = %d, want 1 (timeout match)", len(block.Controls))
	}
	if len(block.KnowledgeInsights) != 1 || len(block.SearchHits) != 1 || len(block.ForesightChains) != 1 || len(block.ApplicableRisks) != 1 {
		t.Errorf("counts: insights=%d hits=%d chains=%d risks=%d", len(block.KnowledgeInsights), len(block.SearchHits), len(block.ForesightChains), len(block.ApplicableRisks))
	}
	if block.SnapshotAt.IsZero() {
		t.Error("SnapshotAt zero")
	}
	// Each Polaris read called once.
	if reader.insightsCalls != 1 || reader.hitsCalls != 1 || reader.chainsCalls != 1 || reader.risksCalls != 1 {
		t.Errorf("call counts: insights=%d hits=%d chains=%d risks=%d", reader.insightsCalls, reader.hitsCalls, reader.chainsCalls, reader.risksCalls)
	}
}

func TestBuildRejectsBadQuery(t *testing.T) {
	reader := &stubReader{}
	b := NewBuilder(reader, nil)
	if _, err := b.Build(context.Background(), Query{}); !errors.Is(err, ErrInvalidQuery) {
		t.Errorf("expected ErrInvalidQuery, got %v", err)
	}
	if reader.insightsCalls != 0 {
		t.Error("Polaris called for invalid query")
	}
}

func TestBuildFailsOnInsightsErr(t *testing.T) {
	reader := &stubReader{insightsErr: errors.New("polaris down")}
	b := NewBuilder(reader, nil)
	_, err := b.Build(context.Background(), Query{IssueID: "g1", Pattern: "timeout"})
	if !errors.Is(err, ErrPolarisFetchFailed) {
		t.Errorf("expected ErrPolarisFetchFailed, got %v", err)
	}
}

func TestBuildDegradesOnSearchAndForesightErr(t *testing.T) {
	reader := &stubReader{
		insights:  []polaris.KnowledgeInsight{{Title: "x"}},
		hitsErr:   errors.New("not configured"),
		chainsErr: errors.New("disabled"),
	}
	b := NewBuilder(reader, nil)
	block, err := b.Build(context.Background(), Query{IssueID: "g1", Pattern: "timeout"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(block.SearchHits) != 0 || len(block.ForesightChains) != 0 {
		t.Errorf("expected empty hits/chains on degrade, got %d/%d", len(block.SearchHits), len(block.ForesightChains))
	}
	if len(block.KnowledgeInsights) != 1 {
		t.Errorf("insights should still be present: %d", len(block.KnowledgeInsights))
	}
}

func TestFilterControlsCaseInsensitive(t *testing.T) {
	catalog := &polaris.ControlsCatalog{Controls: []polaris.Control{
		{ControlCode: "RC-1", Name: "Outbound TIMEOUT"},
		{ControlCode: "RC-2", Name: "Retry hygiene"},
	}}
	q := Query{Pattern: "timeout"}
	got := filterControls(catalog, q)
	if len(got) != 1 || got[0].ControlCode != "RC-1" {
		t.Errorf("filterControls returned %v", got)
	}
}
