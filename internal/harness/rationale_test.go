package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
)

// TestApproveReceivesAssistantRationale (or-10m0): the loop threads the
// assistant's text for the response into the Approve hook, so the approval card
// can show WHY the tool is being run.
func TestApproveReceivesAssistantRationale(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name: "write_file", Safety: tools.Safety{RequiresApproval: true},
		Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil },
	})
	// A response that EXPLAINS itself, then calls the tool.
	explained := &llm.ChatResponse{
		StopReason: llm.StopToolUse,
		Content: []llm.ContentBlock{
			{Type: llm.BlockText, Text: "I'll write the config so the server can boot."},
			{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: "t1", Name: "write_file", Input: json.RawMessage(`{"path":"cfg"}`)}},
		},
		Usage: llm.Usage{InputTokens: 5, OutputTokens: 5},
	}
	prov := &scriptedProvider{resp: []*llm.ChatResponse{explained, endResp("done")}}

	var gotRationale string
	loop := Loop{Provider: prov, Tools: reg,
		Approve: func(_ context.Context, _ string, _ json.RawMessage, _ tools.Safety, rationale string) Decision {
			gotRationale = rationale
			return DecisionAllow
		}}
	if _, _, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotRationale, "so the server can boot") {
		t.Fatalf("Approve must receive the assistant's rationale for the call, got %q", gotRationale)
	}
}
