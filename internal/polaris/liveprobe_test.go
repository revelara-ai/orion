package polaris

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLiveSessionProbe is the or-xe7.9 LIVE validation: refresh (if needed) +
// initialize + tools/list against the real revelara.ai MCP endpoint using the
// developer's stored credential. Opt-in only (ORION_LIVE_PROBE=1): it needs
// network + a cached login, so CI and normal runs skip it.
func TestLiveSessionProbe(t *testing.T) {
	if os.Getenv("ORION_LIVE_PROBE") != "1" {
		t.Skip("live probe is opt-in: set ORION_LIVE_PROBE=1 (needs network + a cached /mcp login)")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	ts, err := NewTokenStore(filepath.Join(home, ".orion", "credentials"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	endpoint := "https://api.revelara.ai/mcp"
	tok, refreshed, err := EnsureFreshToken(ctx, ts, endpoint, time.Now())
	if err != nil {
		t.Fatalf("EnsureFreshToken: %v", err)
	}
	if tok.AccessToken == "" {
		t.Skip("no cached credential — run /mcp login first")
	}
	t.Logf("refreshed=%v client_id_set=%v", refreshed, tok.ClientID != "")
	detail, perr := ProbeSession(ctx, endpoint, tok)
	if perr != nil {
		t.Fatalf("live probe: %v", perr)
	}
	t.Logf("live probe: %s", detail)
}
