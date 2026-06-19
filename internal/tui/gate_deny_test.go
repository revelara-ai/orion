package tui

import (
	"context"
	"github.com/revelara-ai/orion/internal/acp"
	"testing"
)

func TestApprovalGateDefaultDeny(t *testing.T) {
	g := &ApprovalGate{}
	r, _ := g.RequestPermission(context.Background(), acp.PermissionRequest{Kind: "destructive"})
	if r.Outcome != "denied" {
		t.Fatalf("default must deny, got %q", r.Outcome)
	}
}
