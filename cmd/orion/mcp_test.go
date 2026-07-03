package main

import (
	"strings"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/polaris"
	"github.com/revelara-ai/orion/internal/tui"
)

// mcpAuthStatusLine must report token VALIDITY, not mere presence: a cached-but-
// expired token that predates client-id persistence reads as "session expired — run
// /mcp login", not "logged in". This is the defect that made /mcp status claim the
// user was authenticated when the token had lapsed 20h earlier.
func TestMCPAuthStatusLine(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	valid := now.Add(time.Hour).Unix()
	expired := now.Add(-time.Hour).Unix()

	cases := []struct {
		name     string
		tok      polaris.Token
		loggedIn bool
		want     string // substring
		notWant  string
	}{
		{"never logged in", polaris.Token{}, false, "not logged in", "auth: logged in"},
		{"valid session", polaris.Token{AccessToken: "a", Org: "acme", ExpiresAt: valid}, true, "logged in", "expired"},
		{"expired but refreshable", polaris.Token{AccessToken: "a", RefreshToken: "r", ClientID: "c", ExpiresAt: expired}, true, "refresh", ""},
		{"expired unrefreshable", polaris.Token{AccessToken: "a", RefreshToken: "r", ExpiresAt: expired}, true, "session expired", ""},
	}
	for _, c := range cases {
		got := mcpAuthStatusLine(c.tok, c.loggedIn, now)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: %q missing %q", c.name, got, c.want)
		}
		if c.notWant != "" && strings.Contains(got, c.notWant) {
			t.Errorf("%s: %q should not contain %q", c.name, got, c.notWant)
		}
		if c.name == "expired unrefreshable" && !strings.Contains(got, "login") {
			t.Errorf("%s: %q should tell the user to /mcp login", c.name, got)
		}
	}
}

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
	if out, _ := mcpCommandAsync("show"); !strings.Contains(out, "api.revelara.ai/mcp") {
		t.Errorf("after clear, show should fall back to the production default endpoint: %q", out)
	}
}

// TestMCPCommandLoginIsAsync (or-xe7.7): login returns an immediate line + an async tea.Cmd; with no
// endpoint configured the follow-up reports the missing config (rather than hanging on a browser).
func TestMCPCommandLoginIsAsync(t *testing.T) {
	t.Setenv("ORION_DATA_DIR", t.TempDir())
	// Point at an unreachable loopback endpoint so login fails fast at discovery — no prod call, no
	// browser (discovery happens before the browser step).
	t.Setenv("ORION_POLARIS_MCP_URL", "http://127.0.0.1:1/mcp")

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
	if !strings.Contains(res.Text, "mcp login") {
		t.Errorf("a failed login should report an error, got %q", res.Text)
	}
}
