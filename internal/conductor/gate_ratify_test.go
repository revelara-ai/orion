package conductor

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

// fixedGate answers every permission request with a fixed outcome.
type fixedGate struct{ outcome string }

func (g fixedGate) RequestPermission(_ context.Context, _ acp.PermissionRequest) (acp.PermissionResult, error) {
	return acp.PermissionResult{Outcome: g.outcome}, nil
}

func driveToReview(t *testing.T, gate acp.PermissionGate) (*orchestrator.Conductor, context.Context, func(string)) {
	t.Helper()
	store := openStore(t)
	oc := orchestrator.NewWithStore(store)
	clientEnd, agentEnd := net.Pipe()
	t.Cleanup(func() { clientEnd.Close(); agentEnd.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	ca := NewConductorAgent(RoleTemplate{Project: "demo"}, oc)
	go ca.Serve(ctx, agentEnd, agentEnd)
	client := acp.NewClient(clientEnd, clientEnd, gate, nil)
	go client.Run(ctx)
	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	sid, err := client.SessionNew(ctx)
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	var last string
	turn := func(text string) {
		_, err := client.Prompt(ctx, sid, text, func(u acp.Update) {
			if u.Text != "" {
				last = u.Text
			}
		})
		if err != nil {
			t.Fatalf("prompt %q: %v", text, err)
		}
	}
	// Intent + answer the blocking questions; the final answer turn assembles the
	// spec and hits the ratify gate.
	turn("build an http service that returns the current time")
	for i := 0; i < 8; i++ {
		if sv, _ := oc.SpecView(ctx); sv.Status == "accepted" {
			break
		}
		a := answerFor(last)
		if a == "y" { // reached the review/ratify point (the gate already decided); stop
			break
		}
		turn(a)
	}
	return oc, ctx, turn
}

// TestConductorRatifiesViaPermissionGate (or-pp9): with a granting gate, the
// Conductor's blocking session/request_permission(spec_ratify) accepts the spec
// within the turn — no conversational 'y' needed.
func TestConductorRatifiesViaPermissionGate(t *testing.T) {
	oc, ctx, _ := driveToReview(t, fixedGate{outcome: "granted"})
	if sv, _ := oc.SpecView(ctx); sv.Status != "accepted" {
		t.Fatalf("granting gate should ratify the spec; status=%q", sv.Status)
	}
}

// TestConductorDeniedGateDoesNotRatify: a denying gate leaves the spec unaccepted
// (the developer can then edit/ratify).
func TestConductorDeniedGateDoesNotRatify(t *testing.T) {
	oc, ctx, _ := driveToReview(t, fixedGate{outcome: "denied"})
	if sv, _ := oc.SpecView(ctx); sv.Status == "accepted" {
		t.Fatal("denied ratify gate must not accept the spec")
	}
}
