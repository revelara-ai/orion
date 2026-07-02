package conductor

import (
	"context"
	"os"
	"path/filepath"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/polaris"
)

// loadReliabilityContext pulls the project's reliability context (controls / knowledge / risks) from
// the revelara.ai MCP service before the delivery bar (or-xe7.4). It resolves the OAuth credential
// from the data dir (store.Dir()/credentials — where `orion login` caches it, the same layout as the
// CLI's credentialsDir) and the MCP endpoint from $ORION_POLARIS_MCP_URL, else the token's own
// endpoint (an OAuth token carries the MCP endpoint as its BaseURL).
//
// With no credential or endpoint it returns REDUCED context (a nil Consumer serves cache/empty and
// flags Reduced) — the reliability touchpoint is optional, never a hard dependency, so a build never
// fails for lack of it; the reduced flag is recorded in the delivery envelope instead.
func loadReliabilityContext(ctx context.Context, store *contextstore.Store, projectID, query string) polaris.ReliabilityContext {
	var mcp *polaris.MCPClient
	if ts, err := polaris.NewTokenStore(filepath.Join(store.Dir(), "credentials")); err == nil {
		if tok, ok, _ := ts.Load(); ok && tok.AccessToken != "" {
			endpoint := os.Getenv("ORION_POLARIS_MCP_URL")
			if endpoint == "" {
				endpoint = tok.BaseURL
			}
			if endpoint != "" {
				mcp = polaris.NewMCPClient(endpoint, tok.AccessToken)
			}
		}
	}
	rc, _ := polaris.NewConsumer(mcp, store).Load(ctx, projectID, query)
	return rc
}
