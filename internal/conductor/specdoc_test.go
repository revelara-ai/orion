package conductor

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// TestSpecDocument: the rendered document reflects the anchored contract +
// decisions (the artifact of the grill), and marks fallback-resolved decisions.
func TestSpecDocument(t *testing.T) {
	es := spec.ExecutableSpec{
		Intent: "Build an HTTP service that returns the current time.",
		Hash:   "abc123",
		ResponseContract: spec.ResponseContract{
			Route: "/time", Port: 8080, ContentType: "application/json", TimeZone: "UTC",
		},
		Dimensions: []spec.Dimension{
			{Name: "functional", ValueKind: "precise", Values: map[string]string{"response_format": "json", "route": "/time"}},
			{Name: "scale", ValueKind: "fallback_preset", Values: map[string]string{"scale_profile": "low"}},
		},
	}
	doc := SpecDocument(es, true)
	for _, want := range []string{
		"# Spec — Build an HTTP service",
		"abc123",
		"application/json",
		"GET /time",
		"response_format: json",
		"_(fallback)_", // scale resolved via fallback is marked inline
		"proven against this contract",
		// A prominent assumptions section names the fallback-resolved decision so the
		// developer sees what was decided on their behalf.
		"Assumptions — resolved on your behalf",
		"scale_profile",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("spec document missing %q\n---\n%s", want, doc)
		}
	}
}

// TestSpecPreviewSurfacesAssumptions: the PREVIEW document (not yet ratified) leads
// with the assumptions the developer must review, and is framed as a preview — so the
// developer reviews what was decided on their behalf BEFORE ratifying, not after.
func TestSpecPreviewSurfacesAssumptions(t *testing.T) {
	es := spec.ExecutableSpec{
		Intent: "Build a service.",
		Dimensions: []spec.Dimension{
			{Name: "scale", ValueKind: "fallback_preset", Values: map[string]string{"scale_profile": "low"}},
			{Name: "observability", ValueKind: "fallback_preset", Values: map[string]string{"observability_signals": "tier-default"}},
		},
	}
	doc := SpecDocument(es, false)
	if !strings.Contains(doc, "Assumptions — resolved on your behalf") {
		t.Fatalf("preview must surface assumptions:\n%s", doc)
	}
	for _, k := range []string{"scale_profile", "observability_signals"} {
		if !strings.Contains(doc, k) {
			t.Errorf("preview assumptions missing %q\n%s", k, doc)
		}
	}
	if !strings.Contains(doc, "not yet ratified") {
		t.Errorf("preview must be framed as not-yet-ratified, not as ratified:\n%s", doc)
	}
	if strings.Contains(doc, "proven against this contract") {
		t.Errorf("preview must NOT claim it is ratified:\n%s", doc)
	}
}
