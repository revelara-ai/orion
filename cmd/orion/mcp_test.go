package main

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/tui"
)

// TestMCPCommandSetShowClear (or-xe7.7): set persists the endpoint, show reflects it + auth status,
// clear removes it — all synchronous (no tea.Cmd).
func TestMCPCommandSetShowClear(t *testing.T) {
	t.Setenv("ORION_DATA_DIR", t.TempDir())
	t.Setenv("ORION_POLARIS_MCP_URL", "") // no env override

	if out, cmd := mcpCommandAsync("set https://app.revelara.ai/mcp"); cmd != nil || !strings.Contains(out, "set to") {
		t.Fatalf("set: out=%q cmd=%v", out, cmd)
	}
	out, cmd := mcpCommandAsync("show")
	if cmd != nil {
		t.Error("show must be synchronous")
	}
	if !strings.Contains(out, "app.revelara.ai/mcp") || !strings.Contains(out, "not logged in") {
		t.Errorf("show should reflect the set endpoint + auth status: %q", out)
	}
	if out, _ := mcpCommandAsync("clear"); !strings.Contains(out, "cleared") {
		t.Errorf("clear: %q", out)
	}
	if out, _ := mcpCommandAsync("show"); !strings.Contains(out, "not configured") {
		t.Errorf("after clear, show should report unconfigured: %q", out)
	}
}

// TestMCPCommandLoginIsAsync (or-xe7.7): login returns an immediate line + an async tea.Cmd; with no
// endpoint configured the follow-up reports the missing config (rather than hanging on a browser).
func TestMCPCommandLoginIsAsync(t *testing.T) {
	t.Setenv("ORION_DATA_DIR", t.TempDir())
	t.Setenv("ORION_POLARIS_MCP_URL", "")

	immediate, cmd := mcpCommandAsync("login")
	if cmd == nil {
		t.Fatal("login must return an async tea.Cmd")
	}
	if !strings.Contains(immediate, "browser") {
		t.Errorf("immediate line = %q", immediate)
	}
	res, ok := cmd().(tui.CommandResultMsg)
	if !ok {
		t.Fatalf("async follow-up must yield a CommandResultMsg, got %T", cmd())
	}
	if !strings.Contains(res.Text, "endpoint") {
		t.Errorf("login with no endpoint should report the missing config, got %q", res.Text)
	}
}
