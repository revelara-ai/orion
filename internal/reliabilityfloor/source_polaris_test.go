package reliabilityfloor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/revelara-ai/orion/internal/polaris"
)

func TestParseSignalsExtractsFields(t *testing.T) {
	rc := polaris.ReliabilityContext{
		Controls: json.RawMessage(`[{"id":"RC-42","title":"HTTP timeout","severity":"high","summary":"inc-9 outage"}]`),
		Risks:    json.RawMessage(`[{"short_name":"R-7","name":"No retries","severity":"medium","description":"flaky dep"}]`),
	}
	got := parseSignals(rc)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2: %+v", len(got), got)
	}
	var rc42 *Signal
	for i := range got {
		if got[i].ID == "RC-42" {
			rc42 = &got[i]
		}
	}
	if rc42 == nil || rc42.Title != "HTTP timeout" || rc42.Severity != SevHigh || rc42.Source != "control" {
		t.Fatalf("RC-42 parsed wrong: %+v", rc42)
	}
	var r7 *Signal
	for i := range got {
		if got[i].ID == "R-7" {
			r7 = &got[i]
		}
	}
	if r7 == nil || r7.Title != "No retries" || r7.Why != "flaky dep" || r7.Source != "risk" {
		t.Fatalf("R-7 parsed wrong: %+v", r7)
	}
}

func TestParseSignalsUnwrapsResultsEnvelope(t *testing.T) {
	// Real live-MCP shape (or-uvw.9 dogfood, 2026-07-12): tools wrap items in
	// {"results":[...],"total":N}; knowledge "fact" items have statement but no title.
	rc := polaris.ReliabilityContext{
		Knowledge: json.RawMessage(`{"results":[
			{"id":"6b514278","type":"procedure","title":"Configure robust circuit breakers and aggressive timeouts for calls to external dependencies.","statement":"","vertical":"fault-tolerance","confidence":0.9},
			{"id":"4fe0d672","type":"fact","statement":"Load balancers can be configured with appropriate timeouts.","vertical":"fault-tolerance","confidence":0.65}
		],"total":2}`),
		Controls: json.RawMessage(`{"results":[],"total":0}`),
	}
	got := parseSignals(rc)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2: %+v", len(got), got)
	}
	if got[0].Title == "" || got[1].Title == "" {
		t.Fatalf("titles must be populated (statement fallback for facts): %+v", got)
	}
	if got[1].Title != "Load balancers can be configured with appropriate timeouts." {
		t.Fatalf("fact item must use statement as title, got %q", got[1].Title)
	}
	for _, s := range got {
		if s.Source != "knowledge" {
			t.Fatalf("source tag wrong: %+v", s)
		}
	}
}

func TestParseSignalsHandlesGarbage(t *testing.T) {
	got := parseSignals(polaris.ReliabilityContext{Controls: json.RawMessage(`{"not":"an array"}`)})
	if got != nil {
		t.Fatalf("garbage must yield nil, got %v", got)
	}
}

func TestParseSignalsSkipsIncompleteItems(t *testing.T) {
	rc := polaris.ReliabilityContext{
		Knowledge: json.RawMessage(`[{"severity":"high"},{"id":"K-1","title":"Batch big migrations","severity":"low"}]`),
	}
	got := parseSignals(rc)
	if len(got) != 1 || got[0].ID != "K-1" || got[0].Source != "knowledge" {
		t.Fatalf("want only K-1, got %+v", got)
	}
}

func TestPolarisSourceFetchFailsOpen(t *testing.T) {
	var p *PolarisSource
	sigs, err := p.Fetch(context.Background(), "proj", "q")
	if sigs != nil || err != nil {
		t.Fatalf("nil source must fail open (nil,nil), got %v %v", sigs, err)
	}
	sigs, err = (&PolarisSource{}).Fetch(context.Background(), "proj", "q")
	if sigs != nil || err != nil {
		t.Fatalf("nil consumer must fail open (nil,nil), got %v %v", sigs, err)
	}
}
