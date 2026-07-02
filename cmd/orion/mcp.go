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
	if endpoint == "" {
		return "mcp login: no endpoint configured — run /mcp set <url> first"
	}
	clientID := os.Getenv("ORION_WORKOS_CLIENT_ID")
	if clientID == "" {
		return "mcp login: set $ORION_WORKOS_CLIENT_ID (the WorkOS OAuth client), then retry"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	tok, err := polaris.OAuthConfig{MCPEndpoint: endpoint, ClientID: clientID}.Authorize(ctx)
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
	return "logged in to revelara.ai (" + endpoint + ")"
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
	if endpoint == "" {
		b.WriteString("MCP endpoint: (not configured — /mcp set <url>)\n")
	} else {
		fmt.Fprintf(&b, "MCP endpoint: %s\n", endpoint)
	}
	if loggedIn && tok.AccessToken != "" {
		who := "authenticated"
		if tok.Org != "" {
			who = "org " + tok.Org
		}
		fmt.Fprintf(&b, "auth: logged in (%s)", who)
	} else {
		b.WriteString("auth: not logged in (/mcp login)")
	}
	return b.String()
}
