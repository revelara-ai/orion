package conductor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/harness"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/tools"
)

func TestApproverAllowAlwaysShortCircuits(t *testing.T) {
	a := NewOrionAgent(nil, orchestrator.New(), RoleTemplate{})
	var asks int
	ask := func(acp.PermissionRequest) (acp.PermissionResult, error) {
		asks++
		return acp.PermissionResult{Outcome: "allow_always"}, nil
	}
	approve := a.approver("s1", nil, ask)
	dest := tools.Safety{Destructive: true}
	in := json.RawMessage(`{"command":"ls"}`)

	if approve(context.Background(), "bash", in, dest, "") != harness.DecisionAllow {
		t.Fatal("allow_always should allow")
	}
	if asks != 1 {
		t.Fatalf("first call should prompt once, got %d", asks)
	}
	// Second call to the same tool is short-circuited by the allow-always set.
	if approve(context.Background(), "bash", in, dest, "") != harness.DecisionAllow {
		t.Fatal("second call should still allow")
	}
	if asks != 1 {
		t.Errorf("allow-always should skip the prompt, got %d asks", asks)
	}
}

func TestApproverOutcomesMap(t *testing.T) {
	a := NewOrionAgent(nil, orchestrator.New(), RoleTemplate{})
	dest := tools.Safety{Destructive: true}
	mk := func(outcome string) harness.Decision {
		ask := func(acp.PermissionRequest) (acp.PermissionResult, error) {
			return acp.PermissionResult{Outcome: outcome}, nil
		}
		return a.approver("s"+outcome, nil, ask)(context.Background(), "write_file", json.RawMessage(`{"path":"x","content":"y"}`), dest, "")
	}
	if mk("deny") != harness.DecisionDeny {
		t.Error("deny → DecisionDeny")
	}
	if mk("allow_once") != harness.DecisionAllow {
		t.Error("allow_once → DecisionAllow")
	}
}

// A nil ask (headless subagent / no interactive gate) yields no hook, so mutating tools
// aren't user-prompted — they keep their own red-button gating.
func TestApproverNilAskNoHook(t *testing.T) {
	a := NewOrionAgent(nil, orchestrator.New(), RoleTemplate{})
	if a.approver("s1", nil, nil) != nil {
		t.Error("nil ask must yield a nil approve hook")
	}
}

// toolPreview builds a readable, colorizable preview per tool kind.
func TestToolPreview(t *testing.T) {
	if p := toolPreview("bash", json.RawMessage(`{"command":"go test ./..."}`)); !strings.Contains(p, "go test ./...") {
		t.Errorf("bash preview should show the command: %q", p)
	}
	edit := toolPreview("edit_file", json.RawMessage(`{"path":"x.go","old_string":"old","new_string":"new"}`))
	if !strings.Contains(edit, "x.go") || !strings.Contains(edit, "-old") || !strings.Contains(edit, "+new") {
		t.Errorf("edit preview should show path + a -/+ diff: %q", edit)
	}
	write := toolPreview("write_file", json.RawMessage(`{"path":"n.go","content":"package main"}`))
	if !strings.Contains(write, "n.go") || !strings.Contains(write, "+package main") {
		t.Errorf("write preview should show path + added content: %q", write)
	}
}
