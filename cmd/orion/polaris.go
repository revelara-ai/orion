package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/revelara-ai/orion/internal/polaris"
)

// credentialsDir is separate from the Context Store DB and outside any path the
// sandbox binds — so the token is never in the store and never sandbox-reachable.
func credentialsDir() (string, error) {
	dir, err := resolveDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "credentials"), nil
}

func polarisURL(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if u := os.Getenv("ORION_POLARIS_URL"); u != "" {
		return u
	}
	return "https://app.revelara.ai"
}

// polarisMCPURL resolves the revelara.ai MCP endpoint: flag → $ORION_POLARIS_MCP_URL → the
// TUI-persisted config (`/mcp set`) → the production default.
func polarisMCPURL(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	var cfg polaris.Config
	if dir, err := credentialsDir(); err == nil {
		if cs, err := polaris.NewConfigStore(dir); err == nil {
			cfg, _ = cs.Load()
		}
	}
	return polaris.ResolveMCPURL(os.Getenv("ORION_POLARIS_MCP_URL"), cfg, polaris.Token{})
}

// cmdLogin authenticates to Polaris and caches the token (0600, outside the store).
func cmdLogin(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	url := fs.String("url", "", "revelara.ai base URL (or $ORION_POLARIS_URL)")
	token := fs.String("token", "", "set a token directly (headless/short-lived)")
	mcpURL := fs.String("mcp-url", "", "revelara.ai MCP endpoint (or $ORION_POLARIS_MCP_URL; default <base>/mcp)")
	clientID := fs.String("client-id", "", "WorkOS OAuth client id (or $ORION_WORKOS_CLIENT_ID)")
	org := fs.String("org", "", "organization")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	base := polarisURL(*url)

	dir, err := credentialsDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion login:", err)
		return 1
	}
	store, err := polaris.NewTokenStore(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion login:", err)
		return 1
	}

	var tok polaris.Token
	switch {
	case *token != "":
		tok = polaris.Token{AccessToken: *token, BaseURL: base, Org: *org}
	default:
		// Default: WorkOS AuthKit browser OAuth against the revelara.ai MCP service (or-xe7.2). The
		// client id is OPTIONAL — when unset, the flow registers dynamically (RFC 7591 DCR, or-xe7.8).
		mcp := polarisMCPURL(*mcpURL)
		cid := *clientID
		if cid == "" {
			cid = os.Getenv("ORION_WORKOS_CLIENT_ID")
		}
		fmt.Printf("opening your browser to authorize Orion with revelara.ai (%s) ...\n", mcp)
		tok, err = polaris.OAuthConfig{MCPEndpoint: mcp, ClientID: cid}.Authorize(context.Background())
		if err != nil {
			fmt.Fprintln(os.Stderr, "orion login:", err)
			return 1
		}
		tok.Org = *org
	}
	if err := store.Save(tok); err != nil {
		fmt.Fprintln(os.Stderr, "orion login:", err)
		return 1
	}
	fmt.Printf("logged in to %s (credential cached at %s)\n", base, store.Path())
	// or-xe7.9: post-login probe — an org-less JWT only fails at tool-call
	// time (403); say so now while the developer is still at the keyboard.
	if mcp := polarisMCPURL(*mcpURL); mcp != "" && tok.AccessToken != "" {
		pctx, pcancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer pcancel()
		if detail, perr := polaris.ProbeSession(pctx, mcp, tok); perr != nil {
			fmt.Fprintln(os.Stderr, "warning:", perr)
		} else {
			fmt.Println(detail)
		}
	}
	return 0
}

// cmdStatus moved to status.go (or-gik.4): `orion status` now prints the full init banner with
// a live Polaris probe. The Polaris reachability logic lives there as livePolarisCheck.

// cmdLogout erases the cached credential.
func cmdLogout(_ []string) int {
	dir, err := credentialsDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion logout:", err)
		return 1
	}
	store, err := polaris.NewTokenStore(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion logout:", err)
		return 1
	}
	// or-xe7.5: clear the local credential only — an OAuth access token is
	// stateless server-side; the retired REST Logout was a no-op round-trip.
	if err := store.Clear(); err != nil {
		fmt.Fprintln(os.Stderr, "orion logout:", err)
		return 1
	}
	fmt.Println("logged out")
	return 0
}
