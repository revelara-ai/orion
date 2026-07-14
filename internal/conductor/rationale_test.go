package conductor

import (
	"context"
	"encoding/json"
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
