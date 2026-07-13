package polaris

import (
	"context"
	"fmt"
	"strings"
)

// ProbeSession validates a token END TO END right after login/refresh
// (or-xe7.9 item 2): MCP initialize + tools/list against the live endpoint.
// WorkOS-side auth succeeds even when the JWT carries no org_id — the failure
// only appears as a 403 on tool calls, which previously surfaced downstream
// as silently-reduced reliability context. The probe turns that into an
// actionable message at login time instead.
func ProbeSession(ctx context.Context, endpoint string, tok Token) (string, error) {
	c := NewMCPClient(endpoint, tok.AccessToken)
	if _, err := c.Initialize(ctx); err != nil {
		return "", classifyProbeErr(err)
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		return "", classifyProbeErr(err)
	}
	if len(tools) == 0 {
		return "", fmt.Errorf("session authorized but the server exposes no tools — the account may lack an organization/tool grant; contact your revelara.ai admin")
	}
	return fmt.Sprintf("session verified: %d tools available", len(tools)), nil
}

// classifyProbeErr maps the raw transport/RPC error onto what the developer
// should DO — error text is load-bearing UI (the or-4j37 lesson).
func classifyProbeErr(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "status 403") && strings.Contains(strings.ToLower(msg), "organization"):
		return fmt.Errorf("logged in, but this session has NO ORGANIZATION CONTEXT (the JWT lacks org_id) — tool calls will 403. Re-run login choosing an organization, or ask your revelara.ai admin to assign one to this account. (%v)", err)
	case strings.Contains(msg, "status 403"):
		return fmt.Errorf("logged in, but the server refuses tool access (403) — the account may lack an organization or tool grant: %v", err)
	case strings.Contains(msg, "status 401"):
		return fmt.Errorf("the new session was rejected (401) — the token did not take; run /mcp login again: %v", err)
	default:
		return fmt.Errorf("session probe failed (the login itself succeeded; the endpoint may be unreachable): %v", err)
	}
}
