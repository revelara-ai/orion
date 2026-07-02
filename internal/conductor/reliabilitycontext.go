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
	rc, _ := polaris.NewConsumer(mcpClientFromCredentials(store), store).Load(ctx, projectID, query)
	return rc
}

// mcpClientFromCredentials builds an authenticated revelara.ai MCP client from the cached credential
// (store.Dir()/credentials) + the resolved endpoint (env → config → token → default). Returns nil
// when there is no store or no cached token — the caller then runs without the MCP surface.
func mcpClientFromCredentials(store *contextstore.Store) *polaris.MCPClient {
	if store == nil {
		return nil
	}
	credsDir := filepath.Join(store.Dir(), "credentials")
	ts, err := polaris.NewTokenStore(credsDir)
	if err != nil {
		return nil
	}
	tok, ok, _ := ts.Load()
	if !ok || tok.AccessToken == "" {
		return nil
	}
	var cfg polaris.Config
	if cs, cerr := polaris.NewConfigStore(credsDir); cerr == nil {
		cfg, _ = cs.Load()
	}
	endpoint := polaris.ResolveMCPURL(os.Getenv("ORION_POLARIS_MCP_URL"), cfg, tok)
	if endpoint == "" {
		return nil
	}
	return polaris.NewMCPClient(endpoint, tok.AccessToken)
}
