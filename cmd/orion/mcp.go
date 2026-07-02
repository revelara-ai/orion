package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/revelara-ai/orion/internal/polaris"
)

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
	case "login", "logout":
		return "/mcp " + sub + " runs the browser auth flow — for now use `orion " + sub + "`"
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
