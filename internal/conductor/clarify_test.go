package conductor

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

// TestResolveChoice (or-ykz.7): a numbered/value reply to an enumerable decision maps to the
// canonical option value; out-of-range / unrecognized / free-text replies are verbatim.
func TestResolveChoice(t *testing.T) {
	od := completeness.OpenDecision{Key: "response_format", Options: []string{"json", "text"}}
	for _, c := range []struct{ in, want string }{
		{"1", "json"},
		{"2", "text"},
		{"json", "json"},
		{"TEXT", "text"}, // case-insensitive value match
		{" 1 ", "json"},  // trimmed
		{"5", "5"},       // out of range → verbatim (downstream gate re-asks)
		{"yaml", "yaml"}, // unrecognized → verbatim
	} {
		if got := resolveChoice(od, c.in); got != c.want {
			t.Errorf("resolveChoice(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := resolveChoice(completeness.OpenDecision{Key: "port"}, "9090"); got != "9090" {
		t.Errorf("free-text resolveChoice = %q, want 9090", got)
	}
}

// TestAskOneRendersOptions (or-ykz.7): an enumerable decision renders as a numbered
// multiple-choice prompt; a free-text decision does not.
func TestAskOneRendersOptions(t *testing.T) {
	st := &convoState{pending: []completeness.OpenDecision{
		{Dimension: "functional", Question: "What response format?", Options: []string{"json", "text"}},
	}}
	var msg string
	(&ConductorAgent{}).askOne(st, func(u acp.Update) { msg += u.Text })
	for _, want := range []string{"What response format?", "1) json", "2) text", "number or the value"} {
		if !strings.Contains(msg, want) {
			t.Errorf("askOne message missing %q; got:\n%s", want, msg)
		}
	}
	st2 := &convoState{pending: []completeness.OpenDecision{{Dimension: "functional", Question: "Which port?"}}}
	var msg2 string
	(&ConductorAgent{}).askOne(st2, func(u acp.Update) { msg2 += u.Text })
	if strings.Contains(msg2, "1)") {
		t.Errorf("free-text question must not render numbered options; got: %s", msg2)
	}
}
