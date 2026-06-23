package conductor

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// or-3ba.7: the generation prompt is general + case-driven + reliability-focused,
// not a time-service-specific prompt.
func TestGenerationPromptIsGeneralAndCaseDriven(t *testing.T) {
	gs := sandbox.GenSpec{
		Module: "orion-generated/svc", Route: "/now", Port: 9090, Format: "json",
		EntrySymbol: "handleNow",
		Cases: []spec.BehavioralCase{
			{ID: "c1", Request: spec.RequestShape{Method: "GET", Path: "/now"}, Expect: spec.ExpectShape{Status: 200, ContentType: "application/json"}},
		},
	}
	p := GenerationPrompt(gs, "WRITE_HINT_MARKER")

	if !strings.Contains(p, "handleNow") {
		t.Errorf("prompt must use the DECLARED entry symbol; got:\n%s", p)
	}
	if strings.Contains(p, "handleTime") {
		t.Errorf("prompt must NOT hardcode handleTime; got:\n%s", p)
	}
	if !strings.Contains(p, "GET /now") {
		t.Errorf("prompt must render the behavioral cases (the contract); got:\n%s", p)
	}
	if !strings.Contains(strings.ToLower(p), "reliab") {
		t.Errorf("prompt must stress reliability; got:\n%s", p)
	}
	if !strings.Contains(p, "WRITE_HINT_MARKER") {
		t.Errorf("prompt must honor the write hint; got:\n%s", p)
	}
}

// A non-HTTP spec (no route/port, per or-3ba.5) must not leak HTTP/time framing.
func TestGenerationPromptOmitsHTTPFramingWhenNoRoute(t *testing.T) {
	gs := sandbox.GenSpec{Module: "m", EntrySymbol: "run"}
	p := strings.ToLower(GenerationPrompt(gs, "hint"))
	if strings.Contains(p, "serving route") || strings.Contains(p, "timezone") {
		t.Errorf("a non-HTTP prompt must not mention route/timezone framing; got:\n%s", p)
	}
}
