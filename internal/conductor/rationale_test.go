package conductor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/tools"
)

// TestApprovalCardCarriesRationale (or-10m0 S1): a mutating-tool approval prompt
// carries the assistant's rationale for THIS turn, so the developer knows WHY it
// popped up. A tool call with no preceding explanation says so honestly rather
// than showing an empty card.
func TestApprovalCardCarriesRationale(t *testing.T) {
	a := NewOrionAgent(nil, orchestrator.New(), RoleTemplate{})
	var got acp.PermissionRequest
	ask := func(r acp.PermissionRequest) (acp.PermissionResult, error) {
		got = r
		return acp.PermissionResult{Outcome: "allow_once"}, nil
	}
	hook := a.approver("s1", ask)
	sfy := tools.Safety{RequiresApproval: true}

	// With a rationale: the card shows the why.
	hook(context.Background(), "bash", json.RawMessage(`{"command":"rm stale.tmp"}`), sfy,
		"I need to remove the stale temp file before rebuilding")
	if !strings.Contains(got.Rationale, "remove the stale temp file") {
		t.Fatalf("the approval card must carry the assistant's rationale, got %q", got.Rationale)
	}
	if got.Preview == "" {
		t.Fatal("the preview must still be present")
	}

	// No rationale (model went straight to the tool): honest placeholder, not blank.
	hook(context.Background(), "bash", json.RawMessage(`{"command":"ls"}`), sfy, "   ")
	if strings.TrimSpace(got.Rationale) == "" || !strings.Contains(strings.ToLower(got.Rationale), "no explanation") {
		t.Fatalf("a tool call with no rationale must say so, got %q", got.Rationale)
	}
}

// TestSystemPromptRequiresApprovalRationale (or-vfy7): the compiled prompt
// asks for a one-line rationale before approval-gated calls, so the card's
// honest-empty line becomes an anomaly, not the norm. The card itself never
// synthesizes — that stays pinned by TestApprovalCardCarriesRationale above.
func TestSystemPromptRequiresApprovalRationale(t *testing.T) {
	a := &OrionAgent{role: RoleTemplate{}}
	s := a.systemPrompt()
	if !strings.Contains(s, "Tool-call rationale") || !strings.Contains(s, "approval") {
		t.Fatalf("compiled prompt must carry the approval-rationale rule; missing section in:\n%.400s", s)
	}
}

// TestSystemPromptAppendsHotReadRules (or-vfy7): the Conductor prompt gains
// the hot-read rules.md seam the grill and nativegen already have — an edit
// applies on the next turn, no rebuild; an absent file appends nothing.
func TestSystemPromptAppendsHotReadRules(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORION_HARNESS_DIR", dir)
	a := &OrionAgent{role: RoleTemplate{}}

	if s := a.systemPrompt(); strings.Contains(s, "Extra rules (harness config)") {
		t.Fatal("no rules.md → no extra-rules section")
	}
	if err := os.WriteFile(filepath.Join(dir, "rules.md"), []byte("Always answer in haiku."), 0o644); err != nil {
		t.Fatal(err)
	}
	s := a.systemPrompt()
	if !strings.Contains(s, "Extra rules (harness config)") || !strings.Contains(s, "Always answer in haiku.") {
		t.Fatalf("rules.md must hot-append to the Conductor prompt, tail:\n%s", s[max(0, len(s)-200):])
	}
}
