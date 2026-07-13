package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revelara-ai/orion/internal/polaris"
	"github.com/revelara-ai/orion/internal/tui"
)

// mcpCommandAsync is the /mcp entry point injected into the TUI. `login` runs the WorkOS OAuth
// browser flow as async follow-up work (it blocks on a loopback redirect, so it must not run on the
// Update loop); everything else is synchronous and returns immediately.
func mcpCommandAsync(args string) (string, tea.Cmd) {
	if sub, _, _ := strings.Cut(strings.TrimSpace(args), " "); strings.EqualFold(sub, "login") {
		return "opening your browser to authenticate with revelara.ai …", func() tea.Msg {
			return tui.CommandResultMsg{Text: mcpLogin()}
		}
	}
	return mcpCommandText(args), nil
}

// mcpLogin runs the WorkOS AuthKit OAuth flow against the configured endpoint and caches the token.
// Blocking — invoked from a tea.Cmd goroutine, never the Update loop.
func mcpLogin() string {
	dir, err := credentialsDir()
	if err != nil {
		return "mcp login: " + err.Error()
	}
	cs, err := polaris.NewConfigStore(dir)
	if err != nil {
		return "mcp login: " + err.Error()
	}
	cfg, _ := cs.Load()
	endpoint := polaris.ResolveMCPURL(os.Getenv("ORION_POLARIS_MCP_URL"), cfg, polaris.Token{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	// Client id is OPTIONAL — the flow registers dynamically (RFC 7591 DCR) when it's unset, and
	// requests openid+offline_access scopes; no pre-provisioned WorkOS client needed (or-xe7.8).
	tok, err := polaris.OAuthConfig{MCPEndpoint: endpoint, ClientID: os.Getenv("ORION_WORKOS_CLIENT_ID")}.Authorize(ctx)
	if err != nil {
		return "mcp login: " + err.Error()
	}
	ts, err := polaris.NewTokenStore(dir)
	if err != nil {
		return "mcp login: " + err.Error()
	}
	if err := ts.Save(tok); err != nil {
		return "mcp login: " + err.Error()
	}
	// or-xe7.9: probe the session NOW — an org-less JWT authorizes at login
	// but 403s on every tool call; surface that here, not as silently-reduced
	// context later.
	if detail, perr := polaris.ProbeSession(ctx, endpoint, tok); perr != nil {
		return "logged in to revelara.ai (" + endpoint + ") — but: " + perr.Error()
	} else {
		return "logged in to revelara.ai (" + endpoint + ") — " + detail
	}
}

// mcpLogout clears the cached revelara.ai credential.
func mcpLogout() string {
	dir, err := credentialsDir()
	if err != nil {
		return "mcp logout: " + err.Error()
	}
	ts, err := polaris.NewTokenStore(dir)
	if err != nil {
		return "mcp logout: " + err.Error()
	}
	if err := ts.Clear(); err != nil {
		return "mcp logout: " + err.Error()
	}
	return "logged out of revelara.ai"
}

// mcpCommandText handles the /mcp TUI command (or-xe7.7): configure the revelara.ai MCP endpoint and
// show auth status. show/set/clear are synchronous; login/logout are the async variant (Slice B).
func mcpCommandText(args string) string {
	dir, err := credentialsDir()
	if err != nil {
		return "mcp: " + err.Error()
	}
	cs, err := polaris.NewConfigStore(dir)
	if err != nil {
		return "mcp: " + err.Error()
	}
	sub, rest, _ := strings.Cut(strings.TrimSpace(args), " ")
	switch strings.ToLower(sub) {
	case "set":
		url := strings.TrimSpace(rest)
		if url == "" {
			return "usage: /mcp set <url>"
		}
		cfg, _ := cs.Load()
		cfg.MCPURL = url
		if err := cs.Save(cfg); err != nil {
			return "mcp: " + err.Error()
		}
		return "revelara.ai MCP endpoint set to " + url + " — run /mcp login to authenticate"
	case "clear":
		cfg, _ := cs.Load()
		cfg.MCPURL = ""
		if err := cs.Save(cfg); err != nil {
			return "mcp: " + err.Error()
		}
		return "MCP endpoint config cleared (falls back to $ORION_POLARIS_MCP_URL or the token endpoint)"
	case "", "show", "status":
		return mcpStatusText(dir, cs)
	case "logout":
		return mcpLogout()
	default:
		return "usage: /mcp [show | set <url> | clear | login | logout]"
	}
}

// mcpStatusText renders the resolved endpoint + login status (local, no network).
func mcpStatusText(dir string, cs *polaris.ConfigStore) string {
	cfg, _ := cs.Load()
	var tok polaris.Token
	loggedIn := false
	if ts, err := polaris.NewTokenStore(dir); err == nil {
		tok, loggedIn, _ = ts.Load()
	}
	endpoint := polaris.ResolveMCPURL(os.Getenv("ORION_POLARIS_MCP_URL"), cfg, tok)
	var b strings.Builder
	fmt.Fprintf(&b, "MCP endpoint: %s\n", endpoint)
	b.WriteString(mcpAuthStatusLine(tok, loggedIn, time.Now()))
	return b.String()
}

// mcpAuthStatusLine reports auth status by token VALIDITY, not mere presence. A cached
// token whose short-lived access token has lapsed is NOT "logged in": if it can still
// be refreshed silently (has a refresh token + client id) it renews on next use; if it
// cannot (an old credential with no client id) the developer must re-run /mcp login.
func mcpAuthStatusLine(tok polaris.Token, loggedIn bool, now time.Time) string {
	if !loggedIn || tok.AccessToken == "" {
		return "auth: not logged in (/mcp login)"
	}
	who := "authenticated"
	if tok.Org != "" {
		who = "org " + tok.Org
	}
	if !tok.Expired(now) {
		return fmt.Sprintf("auth: logged in (%s)", who)
	}
	if tok.RefreshToken != "" && tok.ClientID != "" {
		return fmt.Sprintf("auth: logged in (%s) · access token expired, refreshes automatically on next use", who)
	}
	return "auth: session expired — run /mcp login to reconnect"
}
