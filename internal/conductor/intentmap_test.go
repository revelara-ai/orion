package conductor

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/brownfield"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
	"github.com/revelara-ai/orion/pkg/llm"
)

// TestMapIntentDirectsWork: intent is mapped onto BOTH models — affected packages
// (grounded), the DETERMINISTIC blast radius of those packages, the functional domains
// they belong to, and the baseline hazards to preserve (grounded against real UCAs).
// Hallucinated package/UCA refs are dropped.
func TestMapIntentDirectsWork(t *testing.T) {
	// handler ← service ← store : a change to handler ripples to service + (no) ...
	// here: store depended on by service depended on by handler → changing service
	// has blast radius {handler}.
	m := brownfield.RepoMap{
		Profile: brownfield.RepoProfile{Mode: brownfield.Brownfield, Languages: []string{"go"}},
		Packages: []brownfield.GoPackage{
			{Dir: "internal/store", Dependents: []string{"internal/service"}},
			{Dir: "internal/service", Imports: []string{"internal/store"}, Dependents: []string{"internal/handler"}},
			{Dir: "internal/handler", Imports: []string{"internal/service"}},
		},
	}
	fm := FunctionalModel{Domains: []Domain{
		{Name: "Persistence", Packages: []string{"internal/store"}},
		{Name: "Business", Packages: []string{"internal/service"}},
	}}
	baseline := stpa.Model{UCAs: []stpa.UCA{
		{ID: "UCA1", ControlAction: "CA1", Hazard: "data not persisted"},
		{ID: "UCA2", ControlAction: "CA2", Hazard: "stale result served"},
	}}

	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "report_intent_mapping", `{
			"affected_packages":["internal/service","internal/ghost"],
			"touched_ucas":["UCA1","UCA9"],
			"rationale":"the change adds validation in the business layer"}`),
	}}

	im, err := MapIntent(context.Background(), prov, "add input validation", m, fm, baseline)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	// grounded affected packages (ghost dropped)
	if len(im.AffectedPackages) != 1 || im.AffectedPackages[0] != "internal/service" {
		t.Fatalf("affected packages not grounded: %+v", im.AffectedPackages)
	}
	// deterministic blast radius: changing service impacts handler
	if len(im.BlastRadius) != 1 || im.BlastRadius[0] != "internal/handler" {
		t.Fatalf("blast radius wrong (should be handler): %+v", im.BlastRadius)
	}
	// affected domain derived from the package
	if len(im.AffectedDomains) != 1 || im.AffectedDomains[0] != "Business" {
		t.Fatalf("affected domain wrong: %+v", im.AffectedDomains)
	}
	// must-preserve grounded against real UCAs (UCA9 dropped, UCA1 kept with its hazard)
	if len(im.MustPreserve) != 1 || im.MustPreserve[0].UCA != "UCA1" || im.MustPreserve[0].Hazard != "data not persisted" {
		t.Fatalf("must-preserve hazards wrong: %+v", im.MustPreserve)
	}
	// ungrounded refs recorded
	joined := strings.Join(im.Ungrounded, ",")
	if !strings.Contains(joined, "internal/ghost") || !strings.Contains(joined, "UCA9") {
		t.Fatalf("ungrounded refs not recorded: %v", im.Ungrounded)
	}
	d := im.Digest()
	for _, want := range []string{"Work direction", "internal/service", "blast radius", "internal/handler", "Must preserve", "data not persisted"} {
		if !strings.Contains(d, want) {
			t.Fatalf("digest missing %q:\n%s", want, d)
		}
	}
}
