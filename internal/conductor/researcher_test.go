package conductor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/pkg/llm"
)

// textResp builds a plain text ChatResponse (an end-turn answer, no tools).
func textResp(s string) *llm.ChatResponse {
	return &llm.ChatResponse{Content: []llm.ContentBlock{{Type: llm.BlockText, Text: s}}, StopReason: llm.StopEndTurn}
}

// TestGrillAgentParsesQuestionMode (or-kjcm.4): the grill's report_questions
// mode classification lands on the OpenDecision; anything but an explicit
// "research" classifies hitl — a judgment call is never auto-answered.
func TestGrillAgentParsesQuestionMode(t *testing.T) {
	fake := &fakeLLM{resp: []*llm.ChatResponse{{Content: []llm.ContentBlock{{
		Type: llm.BlockToolUse,
		ToolUse: &llm.ToolUse{ID: "1", Name: "report_questions", Input: json.RawMessage(
			`{"questions":[
				{"key":"existing_api","question":"What does the endpoint return?","mode":"research"},
				{"key":"audience","question":"Who is this for?","mode":"hitl"},
				{"key":"success","question":"What does success look like?"}
			]}`)},
	}}}}}
	grill := NativeGrillAgent(fake, nil)
	qs, err := grill(context.Background(), "intent", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"grill.existing_api": "research", "grill.audience": "hitl", "grill.success": "hitl"}
	if len(qs) != len(want) {
		t.Fatalf("got %d questions: %+v", len(qs), qs)
	}
	for _, q := range qs {
		if want[q.Key] != q.Mode {
			t.Fatalf("mode for %s = %q, want %q", q.Key, q.Mode, want[q.Key])
		}
	}
}

// TestNativeResearcherReturnsFinding: a lookup that ends with a plain answer
// returns it as the finding; an UNRESOLVED answer errors (downgrade signal).
func TestNativeResearcherReturnsFinding(t *testing.T) {
	c := orchestrator.NewWithStore(openStore(t))
	q := contextstore.OpenQuestion{Key: "grill.existing_api", Question: "What does the endpoint return?", Mode: "research"}

	ok := &fakeLLM{resp: []*llm.ChatResponse{textResp("It returns {\"time\": RFC3339}; see pkg/api/handler.go.")}}
	r := specTools(c, ok, &changeSession{}, nil)
	finding, err := NativeResearcher(ok, r, c, nil)(context.Background(), q)
	if err != nil {
		t.Fatalf("researcher: %v", err)
	}
	if !strings.Contains(finding, "RFC3339") {
		t.Fatalf("finding lost: %q", finding)
	}

	un := &fakeLLM{resp: []*llm.ChatResponse{textResp("UNRESOLVED — the endpoint is not in this repo.")}}
	r2 := specTools(c, un, &changeSession{}, nil)
	if _, err := NativeResearcher(un, r2, c, nil)(context.Background(), q); err == nil {
		t.Fatal("an UNRESOLVED lookup must error so the question downgrades to the developer")
	}
}
