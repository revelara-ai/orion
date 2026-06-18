package orchestrator

import (
	"context"
	"strings"
	"testing"
)

// TestConductorSubmit is the or-0d2 done-gate: the Conductor accepts an intent
// and returns a confirmation that echoes it. This is the thinnest slice of the
// Conductor.Submit contract the rest of the loop thickens.
func TestConductorSubmit(t *testing.T) {
	c := New()
	const intent = "Build an HTTP service that returns the current time."

	conf, err := c.Submit(context.Background(), intent)
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if !conf.Accepted {
		t.Fatalf("expected intent to be accepted, got Accepted=false (%q)", conf.Message)
	}
	if !strings.Contains(conf.Message, intent) {
		t.Fatalf("confirmation should echo the intent; got %q", conf.Message)
	}
	if conf.Intent != intent {
		t.Fatalf("confirmation Intent = %q, want %q", conf.Intent, intent)
	}
}

func TestConductorSubmitRejectsEmptyIntent(t *testing.T) {
	c := New()
	if _, err := c.Submit(context.Background(), "   "); err == nil {
		t.Fatal("expected error for empty/whitespace intent, got nil")
	}
}

func TestConductorSubmitHonorsContextCancellation(t *testing.T) {
	c := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Submit(ctx, "anything"); err == nil {
		t.Fatal("expected error when context is already cancelled, got nil")
	}
}

// TestConductorStatusReflectsSubmittedIntent ensures Status() observes state set
// by Submit — the skeleton of the situational-awareness surface.
func TestConductorStatusReflectsSubmittedIntent(t *testing.T) {
	c := New()
	if got := c.Status().Intent; got != "" {
		t.Fatalf("fresh Conductor Status().Intent = %q, want empty", got)
	}
	const intent = "Build a thing"
	if _, err := c.Submit(context.Background(), intent); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if got := c.Status().Intent; got != intent {
		t.Fatalf("Status().Intent = %q, want %q", got, intent)
	}
}
