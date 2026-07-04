package tui

import (
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
