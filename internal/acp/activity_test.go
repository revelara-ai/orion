// internal/acp/activity_test.go
package acp

import "testing"

func TestActivityUpdate(t *testing.T) {
	u := Activity("research", "web_search", 1, "running")
	if u.Kind != ActivityKind {
		t.Fatalf("Kind = %q, want %q", u.Kind, ActivityKind)
	}
	if u.Actor != "research" || u.Text != "web_search" || u.Depth != 1 || u.Status != "running" {
		t.Fatalf("unexpected fields: %+v", u)
	}
}
