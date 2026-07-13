package conductor

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
)

// TestIntakeVocabularyNeutral (or-hn15.6 DONE-WHEN a+c+d): the always-on intake
// coaching and tool descriptions are not HTTP/Go-only — record_answer names the
// real format set (incl. xml, not "the only formats"), acknowledge_reduced_proof
// frames a neutral trade-off, and the flow says "build the project".
func TestIntakeVocabularyNeutral(t *testing.T) {
	c := orchestrator.NewWithStore(openStore(t))
	r := specTools(c, nil, &changeSession{}, nil)

	ra, ok := r.Get("record_answer")
	if !ok {
		t.Fatal("record_answer not registered")
	}
	if strings.Contains(ra.Description, "the only formats") {
		t.Fatalf("record_answer must not claim json/plain-text are the only formats (xml is supported): %s", ra.Description)
	}
	if !strings.Contains(strings.ToLower(ra.Description), "xml") {
		t.Fatalf("record_answer should name xml among the supported formats: %s", ra.Description)
	}

	arp, ok := r.Get("acknowledge_reduced_proof")
	if !ok {
		t.Fatal("acknowledge_reduced_proof not registered")
	}
	if strings.Contains(arp.Description, "exceeds what the proof harness can prove") {
		t.Fatalf("acknowledge_reduced_proof should frame a neutral recorded trade-off, not 'exceeds the harness': %s", arp.Description)
	}

	// The always-on system prompt frames the build as "the project", not "the service".
	ag := NewOrionAgent(nil, c, RoleTemplate{Project: "demo"})
	if strings.Contains(ag.systemPrompt(), "build the service to the spec") {
		t.Fatal("the intake prompt must say 'build the project', not 'build the service'")
	}
}
