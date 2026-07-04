package tui

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/acp"
)

func TestActivityModelStackAndPhases(t *testing.T) {
	a := newActivityModel()
	a.apply(acp.Activity("Orion", "build_service", 0, "running"))
	a.apply(acp.Activity("Orion", "generate", 0, "done"))
	a.apply(acp.Activity("research", "web_search", 1, "running"))

	if len(a.stack) == 0 || a.stack[len(a.stack)-1].actor != "research" {
		t.Fatalf("deepest frame should be the subagent; stack=%+v", a.stack)
	}
	if !hasPhase(a.phases, "generate", "done") {
		t.Fatalf("generate phase not recorded done: %+v", a.phases)
	}

	a.apply(acp.Activity("research", "", 1, "done")) // subagent resolves
	if deepestActor(a.stack) == "research" {
		t.Fatalf("resolved subagent should be popped; stack=%+v", a.stack)
	}
}

func TestActivityPhaseDedupAndFailSummary(t *testing.T) {
	// --- EqualFold dedup: capitalized production-style phase ---
	a := newActivityModel()
	a.apply(acp.Activity("Orion", "Generate", 0, "running"))
	a.apply(acp.Activity("Orion", "Generate", 0, "done"))

	var generatePhases []phaseMark
	for _, p := range a.phases {
		if p.name == "Generate" {
			generatePhases = append(generatePhases, p)
		}
	}
	if len(generatePhases) != 1 {
		t.Fatalf("expected exactly 1 phaseMark named 'Generate', got %d: %+v", len(generatePhases), a.phases)
	}
	if generatePhases[0].status != "done" {
		t.Fatalf("expected Generate status 'done', got %q", generatePhases[0].status)
	}

	// --- Fail summary: finish() must lead with ✗ not ✓ ---
	a.apply(acp.Activity("Orion", "Prove", 0, "fail"))
	a.finish()

	if !strings.Contains(a.summary, "✗") {
		t.Fatalf("summary should contain ✗ for failed phase; got %q", a.summary)
	}
	if !strings.Contains(a.summary, "Prove") {
		t.Fatalf("summary should name the failed phase 'Prove'; got %q", a.summary)
	}
	if strings.HasPrefix(a.summary, "✓") {
		t.Fatalf("summary must not start with ✓ when a phase failed; got %q", a.summary)
	}
}

func hasPhase(phases []phaseMark, name, status string) bool {
	for _, p := range phases {
		if p.name == name && p.status == status {
			return true
		}
	}
	return false
}

func deepestActor(stack []actorFrame) string {
	if len(stack) == 0 {
		return ""
	}
	return stack[len(stack)-1].actor
}
