package testsynth

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// Default keeps the historical HTTP-family symbol so existing callers are
// byte-identical.
func TestDefaultEntrySymbol(t *testing.T) {
	if DefaultEntrySymbol != "handleTime" {
		t.Fatalf("DefaultEntrySymbol = %q, want handleTime", DefaultEntrySymbol)
	}
	if got := (Contract{}).Entry(); got != "handleTime" {
		t.Fatalf("undeclared Contract.Entry() = %q, want handleTime", got)
	}
	if got := (Contract{EntrySymbol: "x"}).Entry(); got != "x" {
		t.Fatalf("declared Contract.Entry() = %q, want x", got)
	}
}

// The cases-driven corpus invokes the DECLARED entry symbol, not the hardwired
// handleTime.
func TestCorpusUsesDeclaredEntrySymbol_Cases(t *testing.T) {
	c := Contract{
		Route:       "/x",
		Format:      "json",
		EntrySymbol: "handleRequest",
		Cases: []spec.BehavioralCase{{
			ID:      "c1",
			Request: spec.RequestShape{Method: "GET", Path: "/x"},
			Expect:  spec.ExpectShape{Status: 200},
		}},
	}
	corpus := SynthesizeBehavioral(c)
	if !strings.Contains(corpus, "handleRequest(w, req)") {
		t.Fatalf("corpus does not invoke the declared entry symbol:\n%s", corpus)
	}
	if strings.Contains(corpus, "handleTime(") {
		t.Fatalf("corpus still references hardwired handleTime:\n%s", corpus)
	}
}

// The legacy (no-cases) corpus also honors the declared symbol.
func TestLegacyCorpusUsesDeclaredEntrySymbol(t *testing.T) {
	corpus := SynthesizeBehavioral(Contract{Route: "/x", Format: "json", EntrySymbol: "serve"})
	if !strings.Contains(corpus, "serve(w, req)") {
		t.Fatalf("legacy corpus does not invoke the declared symbol:\n%s", corpus)
	}
	if strings.Contains(corpus, "handleTime(") {
		t.Fatalf("legacy corpus still references hardwired handleTime:\n%s", corpus)
	}
}
