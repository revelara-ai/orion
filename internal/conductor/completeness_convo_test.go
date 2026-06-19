package conductor

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

func answerFor(q string) string {
	ql := strings.ToLower(q)
	if strings.Contains(ql, "ratify") || strings.Contains(ql, "review the spec") {
		return "y"
	}
	switch {
	case strings.Contains(ql, "format"):
		return "json"
	case strings.Contains(ql, "timezone"):
		return "UTC"
	case strings.Contains(ql, "port"):
		return "8080"
	case strings.Contains(ql, "route"), strings.Contains(ql, "path"):
		return "/time"
	default:
		return "unspecified"
	}
}

// TestConductorAgentDrivesCompletenessToRatify: the Conductor agent runs the
// completeness conversation over ACP — intent, then one question per turn, each
// answer recorded and advancing — until the spec ratifies. This is or-owz: the
// questioning is an agent skill the Conductor owns, not a static checklist dump.
func TestConductorAgentDrivesCompletenessToRatify(t *testing.T) {
	store := openStore(t)
	oc := orchestrator.NewWithStore(store)

	clientEnd, agentEnd := net.Pipe()
	defer clientEnd.Close()
	defer agentEnd.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ca := NewConductorAgent(RoleTemplate{Project: "demo"}, oc)
	go ca.Serve(ctx, agentEnd, agentEnd)

	client := acp.NewClient(clientEnd, clientEnd, nil, nil)
	go client.Run(ctx)
	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	sid, err := client.SessionNew(ctx)
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}

	// One prompt per turn; capture the last message streamed each turn.
	var last string
	turn := func(text string) {
		last = ""
		if _, err := client.Prompt(ctx, sid, text, func(u acp.Update) {
			if u.Text != "" {
				last = u.Text
			}
		}); err != nil {
			t.Fatalf("prompt %q: %v", text, err)
		}
	}

	turn("build an http service that returns the current time") // intent
	ratified := strings.Contains(last, "ratified")
	seen := map[string]int{}
	for i := 0; i < 8 && !ratified; i++ {
		q := last
		seen[q]++
		if seen[q] > 1 {
			t.Fatalf("the same question repeated — answers not advancing: %q", q)
		}
		turn(answerFor(q))
		if strings.Contains(last, "ratified") {
			ratified = true
		}
	}
	if !ratified {
		t.Fatalf("conversation did not ratify; last=%q", last)
	}

	// The spec really is accepted in the store (answers persisted, not just echoed).
	sv, err := oc.SpecView(ctx)
	if err != nil {
		t.Fatalf("spec view: %v", err)
	}
	if sv.Status != "accepted" {
		t.Fatalf("spec status = %q, want accepted", sv.Status)
	}
}
