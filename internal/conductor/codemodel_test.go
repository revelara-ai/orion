package conductor

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/brownfield"
	"github.com/revelara-ai/orion/pkg/llm"
)

// TestAnalyzeFunctionalModelGrounds: the analyzer extracts the model's domains AND
// GROUNDS them against the real RepoMap — a package the model names that doesn't exist
// is dropped (recorded in Ungrounded), while real packages survive. The interpretation
// is the model's; the structure stays accurate.
func TestAnalyzeFunctionalModelGrounds(t *testing.T) {
	m := brownfield.RepoMap{
		Profile:  brownfield.RepoProfile{Mode: brownfield.Brownfield, Languages: []string{"go"}},
		Module:   "example.com/p",
		Packages: []brownfield.GoPackage{{Name: "proof", Dir: "internal/proof"}, {Name: "spec", Dir: "internal/spec"}},
	}
	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "report_functional_model", `{"summary":"a proof harness","domains":[`+
			`{"name":"Proof","purpose":"verify code","packages":["internal/proof","internal/ghost"]},`+
			`{"name":"Spec","purpose":"elicit specs","packages":["internal/spec"]}]}`),
	}}

	fm, err := AnalyzeFunctionalModel(context.Background(), prov, m)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if fm.Summary != "a proof harness" || len(fm.Domains) != 2 {
		t.Fatalf("model not extracted: %+v", fm)
	}
	// hallucinated package dropped + recorded
	for _, d := range fm.Domains {
		for _, p := range d.Packages {
			if p == "internal/ghost" {
				t.Fatalf("hallucinated package survived grounding: %+v", d)
			}
		}
	}
	if !strings.Contains(strings.Join(fm.Ungrounded, ","), "internal/ghost") {
		t.Fatalf("dropped package not recorded in Ungrounded: %v", fm.Ungrounded)
	}
	// real package survives
	var proof *Domain
	for i := range fm.Domains {
		if fm.Domains[i].Name == "Proof" {
			proof = &fm.Domains[i]
		}
	}
	if proof == nil || len(proof.Packages) != 1 || proof.Packages[0] != "internal/proof" {
		t.Fatalf("real package was wrongly dropped: %+v", proof)
	}
	if d := fm.Digest(); !strings.Contains(d, "Proof") || !strings.Contains(d, "Functional model") {
		t.Fatalf("digest missing content:\n%s", d)
	}
}
