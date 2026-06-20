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
	doc := SpecDocument(es)
	for _, want := range []string{
		"# Spec — Build an HTTP service",
		"abc123",
		"application/json",
		"GET /time",
		"response_format: json",
		"_(fallback)_", // scale resolved via fallback is marked
		"proven against this contract",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("spec document missing %q\n---\n%s", want, doc)
		}
	}
}
