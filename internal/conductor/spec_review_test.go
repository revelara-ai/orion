package conductor

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

// TestConductorPresentsSpecForReviewBeforeRatify (or-owo): after the questions the
// Conductor PRESENTS the assembled spec and does NOT accept it until the developer
// ratifies; an edit changes a field and re-presents; only 'y' accepts.
func TestConductorPresentsSpecForReviewBeforeRatify(t *testing.T) {
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

	var lastText string
	specShown := false
	turn := func(text string) {
		if _, err := client.Prompt(ctx, sid, text, func(u acp.Update) {
			if u.Kind == "spec" {
				specShown = true
			}
			if u.Text != "" {
				lastText = u.Text
			}
		}); err != nil {
			t.Fatalf("prompt %q: %v", text, err)
		}
	}

	// Intent, then answer the blocking questions until the spec is presented.
	turn("build an http service that returns the current time")
	for i := 0; i < 8 && !specShown; i++ {
		turn(answerFor(lastText))
	}
	if !specShown {
		t.Fatalf("spec was never presented for review; last=%q", lastText)
	}
	// The spec must NOT be accepted yet — it's under review.
	if sv, _ := oc.SpecView(ctx); sv.Status == "accepted" {
		t.Fatal("spec was accepted before the developer ratified it")
	}

	// Edit a field → spec re-presented, still not accepted.
	specShown = false
	turn("port 9090")
	if !specShown {
		t.Fatalf("editing a field did not re-present the spec; last=%q", lastText)
	}
	if sv, _ := oc.SpecView(ctx); sv.Status == "accepted" {
		t.Fatal("spec accepted after an edit (should still be under review)")
	}

	// Ratify.
	turn("y")
	if sv, _ := oc.SpecView(ctx); sv.Status != "accepted" {
		t.Fatalf("spec not accepted after ratify; status=%q", sv.Status)
	}
	// The edit took effect.
	es, err := oc.RecallSpec(ctx)
	if err != nil {
		t.Fatalf("recall spec: %v", err)
	}
	if es.ResponseContract.Port != 9090 {
		t.Fatalf("edited port not applied: got %d, want 9090", es.ResponseContract.Port)
	}
}
