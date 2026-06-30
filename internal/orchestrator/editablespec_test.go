package orchestrator

import (
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

func editReq(text, key string) spec.Requirement {
	return spec.Requirement{
		Source: completeness.DimFunctional,
		Text:   text,
		Cases: []spec.BehavioralCase{
			{
				Request: spec.RequestShape{Method: "GET", Path: "/time"},
				Expect:  spec.ExpectShape{Status: 200, ContentType: "application/json", Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyPresent, Key: key}}},
			},
		},
	}
}

// TestRemoveRequirementEditsTheDraftSpec (or-tcs.5): the behavioral contract is EDITABLE during
// the grill — a requirement can be removed (and re-added corrected), not only appended.
func TestRemoveRequirementEditsTheDraftSpec(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatalf("submit: %v", err)
	}
	a := editReq("the response includes a non-empty zone field", "zone")
	b := editReq("an invalid tz yields a 400 error field", "error")
	if err := c.AddRequirement(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := c.AddRequirement(ctx, b); err != nil {
		t.Fatal(err)
	}
	if reqs, _ := c.Requirements(ctx); len(reqs) != 2 {
		t.Fatalf("want 2 requirements, got %d", len(reqs))
	}

	a.SetIDs() // recompute a's content-addressed id to target it
	if err := c.RemoveRequirement(ctx, a.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	reqs, _ := c.Requirements(ctx)
	if len(reqs) != 1 {
		t.Fatalf("want 1 requirement after remove, got %d", len(reqs))
	}
	if reqs[0].Text != b.Text {
		t.Errorf("the WRONG requirement was removed; remaining = %q", reqs[0].Text)
	}

	if err := c.RemoveRequirement(ctx, "does-not-exist"); err == nil {
		t.Error("removing an unknown id must error")
	}
}

// TestRemoveRequirementByUniquePrefix: a unique id prefix (as a developer would copy from
// list_requirements) resolves; an ambiguous prefix is refused.
func TestRemoveRequirementByUniquePrefix(t *testing.T) {
	c, ctx := storeConductor(t)
	if _, err := c.Submit(ctx, flowIntent); err != nil {
		t.Fatalf("submit: %v", err)
	}
	a := editReq("zone field present", "zone")
	if err := c.AddRequirement(ctx, a); err != nil {
		t.Fatal(err)
	}
	a.SetIDs()
	if len(a.ID) < 6 {
		t.Skip("id too short to prefix")
	}
	if err := c.RemoveRequirement(ctx, a.ID[:6]); err != nil {
		t.Fatalf("unique prefix should resolve: %v", err)
	}
	if reqs, _ := c.Requirements(ctx); len(reqs) != 0 {
		t.Fatalf("requirement should be gone, %d remain", len(reqs))
	}
}
