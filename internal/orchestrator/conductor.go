// Package orchestrator implements the Conductor — the single orchestrator the
// developer converses with. This is the V2.0 skeleton (or-0d2): it accepts an
// intent and returns a confirmation. Later tasks thicken it with the
// completeness gate, dispatch, truth alignment, drift re-anchoring, the circuit
// breaker, and the deployment-bar decision (PRD: Modules — orchestrator).
//
// Manifesto: the Conductor is the opinionated agentic driver of the SDLC. It is
// deliberately a narrow control-plane object, not a god-object — proof,
// decomposition, and integration live in their own modules.
package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// Confirmation is the Conductor's acknowledgement of a submitted intent. In the
// skeleton it echoes the intent; once the completeness gate lands, Accepted will
// gate on whether intake succeeded and Message will carry the first clarifying
// questions.
type Confirmation struct {
	Intent   string
	Accepted bool
	Message  string
}

// Status is the situational-awareness snapshot the TUI's Conversation/Fleet
// panes render. Skeleton: just the current intent.
type Status struct {
	Intent string
}

// Decision is a developer's answer to an open decision raised by the
// completeness gate. The skeleton records it; the gate consumes it later.
type Decision struct {
	Key   string
	Value string
}

// Conductor owns intent intake. Safe for concurrent use.
type Conductor struct {
	log *slog.Logger

	mu     sync.RWMutex
	intent string
}

// New returns a Conductor ready to accept an intent. It self-instruments via the
// default structured logger (3 a.m. test).
func New() *Conductor {
	return &Conductor{log: slog.Default()}
}

// Submit intakes a developer intent and returns a confirmation. It honors
// context cancellation (every Conductor entry point is cancellable so a hung
// run can be interrupted) and rejects an empty intent rather than silently
// guessing.
func (c *Conductor) Submit(ctx context.Context, intent string) (Confirmation, error) {
	if err := ctx.Err(); err != nil {
		return Confirmation{}, fmt.Errorf("submit cancelled: %w", err)
	}
	trimmed := strings.TrimSpace(intent)
	if trimmed == "" {
		return Confirmation{}, fmt.Errorf("intent is empty: describe what you want to build")
	}

	c.mu.Lock()
	c.intent = trimmed
	c.mu.Unlock()

	c.log.InfoContext(ctx, "intent submitted", "intent", trimmed)

	return Confirmation{
		Intent:   trimmed,
		Accepted: true,
		Message:  fmt.Sprintf("Got it — I'll build: %s", trimmed),
	}, nil
}

// Answer records a developer's answer to an open decision. Skeleton: validated
// and accepted; the completeness gate consumes answers in a later task.
func (c *Conductor) Answer(ctx context.Context, d Decision) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("answer cancelled: %w", err)
	}
	if strings.TrimSpace(d.Key) == "" {
		return fmt.Errorf("decision key is empty")
	}
	c.log.InfoContext(ctx, "decision answered", "key", d.Key, "value", d.Value)
	return nil
}

// Status returns the current situational-awareness snapshot.
func (c *Conductor) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return Status{Intent: c.intent}
}

// Interrupt is the developer's "change direction" / abort hook. Skeleton: a
// no-op that exists so the TUI can wire the control now; later it triggers the
// same transactional rollback path as the reversibility gate.
func (c *Conductor) Interrupt() {
	c.log.Info("conductor interrupted")
}
