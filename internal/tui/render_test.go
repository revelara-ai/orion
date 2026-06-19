package tui

import (
	"strings"
	"testing"
)

// TestRichRenderingDistinguishesKinds: the transcript renders a developer turn, a
// question, a bordered spec-review card, and a plan/ratified line distinctly —
// it's a chat, not a flat echo log (or-2r8).
func TestRichRenderingDistinguishesKinds(t *testing.T) {
	m := Conversation{width: 80, msgs: []msg{
		{role: "you", text: "build a time service"},
		{role: "orion", kind: "agent_message", text: "[functional] Which port should it listen on?"},
		{role: "orion", kind: "spec", text: "functional   port=8080, route=/time"},
		{role: "orion", kind: "plan", text: "Spec ratified ✓ route=/time"},
	}}
	out := m.renderTranscript()

	if !strings.Contains(out, "spec — review") {
		t.Fatalf("spec card title missing — spec not rendered as a card:\n%s", out)
	}
	if !strings.Contains(out, "port=8080") {
		t.Fatalf("spec content missing:\n%s", out)
	}
	if !strings.Contains(out, "Which port") {
		t.Fatalf("question text missing:\n%s", out)
	}
	if !strings.Contains(out, "ratified") {
		t.Fatalf("plan/ratified line missing:\n%s", out)
	}
	if !strings.Contains(out, "you") {
		t.Fatalf("developer turn label missing:\n%s", out)
	}
	if !strings.Contains(out, "Orion") {
		t.Fatalf("orion turn label missing:\n%s", out)
	}
}
