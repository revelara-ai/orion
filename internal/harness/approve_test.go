package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/tools"
)

// A destructive tool denied by the Approve hook is NOT dispatched, and a denial message
// is fed back to the model so it can adapt.
func TestApproveHookDeniesDestructiveTool(t *testing.T) {
	reg := tools.NewRegistry()
	var ran bool
	reg.Register(tools.Tool{
		Name: "write_file", Safety: tools.Safety{RequiresApproval: true},
		Run: func(context.Context, json.RawMessage) (string, error) { ran = true; return "wrote", nil },
	})
	prov := &scriptedProvider{resp: []*llm.ChatResponse{
		toolUseResp("t1", "write_file", `{"path":"x"}`),
		endResp("understood"),
	}}
	var consulted []string
	loop := Loop{Provider: prov, Tools: reg, System: "x",
		Approve: func(_ context.Context, name string, _ json.RawMessage, _ tools.Safety) Decision {
			consulted = append(consulted, name)
			return DecisionDeny
		}}
	convo, _, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Error("a denied tool must NOT run")
	}
	if len(consulted) != 1 || consulted[0] != "write_file" {
		t.Errorf("Approve should be consulted for the destructive tool, got %v", consulted)
	}
	if tr := convo[2].Content[0].ToolResult; tr == nil || !tr.IsError || !strings.Contains(tr.Content, "denied") {
		t.Errorf("denial not fed back to the model as an error result: %+v", tr)
	}
}

// The Approve hook is consulted ONLY for destructive tools; read-only tools run without
// prompting. Allowed tools dispatch normally.
func TestApproveHookAllowsAndSkipsReadOnly(t *testing.T) {
	reg := tools.NewRegistry()
	var wrote, read bool
	reg.Register(tools.Tool{Name: "write_file", Safety: tools.Safety{RequiresApproval: true},
		Run: func(context.Context, json.RawMessage) (string, error) { wrote = true; return "ok", nil }})
	reg.Register(tools.Tool{Name: "read_file", Safety: tools.Safety{ReadOnly: true},
		Run: func(context.Context, json.RawMessage) (string, error) { read = true; return "data", nil }})
	prov := &scriptedProvider{resp: []*llm.ChatResponse{
		toolUseResp("t1", "read_file", `{}`),
		toolUseResp("t2", "write_file", `{}`),
		endResp("done"),
	}}
	var consulted []string
	loop := Loop{Provider: prov, Tools: reg,
		Approve: func(_ context.Context, name string, _ json.RawMessage, _ tools.Safety) Decision {
			consulted = append(consulted, name)
			return DecisionAllow
		}}
	if _, _, err := loop.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil); err != nil {
		t.Fatal(err)
	}
	if !read || !wrote {
		t.Error("allowed tools should both run")
	}
	if len(consulted) != 1 || consulted[0] != "write_file" {
		t.Errorf("Approve should be consulted ONLY for the destructive tool, got %v", consulted)
	}
}
