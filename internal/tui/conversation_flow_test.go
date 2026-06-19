package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

// enter drives one Enter keypress with the given input text and returns the next
// model state (Update is value-receiver; we cast the returned model back).
func enter(m Conversation, text string) Conversation {
	m.input.SetValue(text)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return next.(Conversation)
}

// TestConversationRecordsAnswersAndAdvances: an intent enters the grill phase;
// each answer is recorded and the NEXT question is shown (the same question never
// repeats); once the blocking questions are answered the spec is ratified. This
// is the regression for or-lut (answers used to never register).
func TestConversationRecordsAnswersAndAdvances(t *testing.T) {
	store, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	c := orchestrator.NewWithStore(store)
	m := NewConversation(c)

	// 1) Submit the intent → grill phase with blocking questions queued.
	m = enter(m, "build an http service that returns the current time")
	if m.phase != phaseGrill {
		t.Fatalf("after intent: phase=%d, want grill (%d)", m.phase, phaseGrill)
	}
	if len(m.pending) == 0 {
		t.Fatal("no blocking questions queued after intent")
	}

	// 2) Answer the blocking questions one at a time. Each answer must advance.
	answers := map[string]string{
		"response_format": "json", "timezone": "UTC", "port": "8080", "route": "/time",
	}
	guard := 0
	for m.phase == phaseGrill {
		od := m.pending[0]
		prevKey := od.Key
		prevLen := len(m.pending)

		v := answers[od.Key]
		if v == "" {
			v = "unspecified-but-nonempty"
		}
		m = enter(m, v)

		guard++
		if guard > 12 {
			t.Fatal("grill never terminated — answers are not registering (or-lut regression)")
		}
		// If still grilling, the queue must have shrunk and not be stuck on the
		// same question.
		if m.phase == phaseGrill && len(m.pending) >= prevLen && m.pending[0].Key == prevKey {
			t.Fatalf("answer to %q did not register — the same question repeats", prevKey)
		}
	}

	// 3) Spec ratified, and answers were persisted to the store.
	if m.phase != phaseReady {
		t.Fatalf("after answering: phase=%d, want ready (%d)", m.phase, phaseReady)
	}
	sv, err := c.SpecView(context.Background())
	if err != nil {
		t.Fatalf("spec view: %v", err)
	}
	if sv.Status != "accepted" {
		t.Fatalf("spec status = %q, want accepted (answers + approve did not persist)", sv.Status)
	}
}

// TestConversationGrillRequiresAnswer: an empty Enter during the grill does not
// advance (a blocking question has no safe default).
func TestConversationGrillRequiresAnswer(t *testing.T) {
	store, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	c := orchestrator.NewWithStore(store)
	m := NewConversation(c)

	m = enter(m, "build an http service that returns the current time")
	if m.phase != phaseGrill {
		t.Fatalf("want grill, got %d", m.phase)
	}
	before := m.pending[0].Key
	m = enter(m, "   ") // whitespace-only → must not advance
	if m.phase != phaseGrill || m.pending[0].Key != before {
		t.Fatal("empty answer must not advance a blocking question")
	}
}
