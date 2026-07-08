package conductor

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/brownfield"
	"github.com/revelara-ai/orion/pkg/llm"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
)

// TestAnalyzeSTAMPBaselineMaps: the proposer maps the model's report onto a stpa.Model
// — losses, control structure (with feedback), and UCAs with a normalized type, the
// Verify tokens, and an OPEN disposition (the developer ratifies before it anchors). A
// bad UCA type falls back to not_provided.
func TestAnalyzeSTAMPBaselineMaps(t *testing.T) {
	m := brownfield.RepoMap{
		Profile:  brownfield.RepoProfile{Mode: brownfield.Brownfield, Languages: []string{"go"}},
		Packages: []brownfield.GoPackage{{Name: "handler", Dir: "internal/handler"}},
	}
	fm := FunctionalModel{Summary: "a request service", Domains: []Domain{{Name: "Handler", Purpose: "serve", Packages: []string{"internal/handler"}}}}

	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "report_stamp_baseline", `{
			"losses":[{"id":"L1","description":"clients get wrong results"}],
			"controllers":["Request Handler"],
			"control_actions":[{"id":"CA1","controller":"Request Handler","action":"serve response","feedback_from":"handler","feedback_to":"metrics","feedback_signal":"status code"}],
			"ucas":[
				{"id":"UCA1","control_action":"CA1","type":"provided_incorrectly","hazard":"wrong response returned","loss_refs":["L1"],"verify":["validateInput"]},
				{"id":"UCA2","control_action":"CA1","type":"bogus-type","hazard":"x","loss_refs":["L1"]}
			]}`),
	}}

	model, err := AnalyzeSTAMPBaseline(context.Background(), prov, m, fm)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if len(model.Losses) != 1 || model.Losses[0].ID != "L1" {
		t.Fatalf("losses not mapped: %+v", model.Losses)
	}
	if len(model.Structure.Actions) != 1 || model.Structure.Actions[0].Feedback.Signal != "status code" {
		t.Fatalf("control action / feedback not mapped: %+v", model.Structure.Actions)
	}
	if len(model.UCAs) != 2 {
		t.Fatalf("want 2 UCAs: %+v", model.UCAs)
	}
	u1 := model.UCAs[0]
	if u1.Type != stpa.ProvidedIncorrectly || u1.Disposition != stpa.DispositionOpen {
		t.Fatalf("UCA1 type/disposition wrong: %+v", u1)
	}
	if len(u1.Verify) != 1 || u1.Verify[0] != "validateInput" {
		t.Fatalf("UCA1 verify tokens not mapped: %+v", u1.Verify)
	}
	if model.UCAs[1].Type != stpa.NotProvided { // bad type normalized
		t.Fatalf("bad UCA type should normalize to not_provided: %q", model.UCAs[1].Type)
	}

	r := RenderBaseline(model)
	for _, want := range []string{"STAMP baseline", "L1", "CA1", "UCA1", "validateInput", "ratify"} {
		if !strings.Contains(r, want) {
			t.Fatalf("render missing %q:\n%s", want, r)
		}
	}
}
