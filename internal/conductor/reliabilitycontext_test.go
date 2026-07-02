package conductor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/polaris"
)

// TestLoadReliabilityContextReducedWithoutCredential (or-xe7.4): with no cached credential the helper
// returns REDUCED context — the build never hard-depends on revelara.ai.
func TestLoadReliabilityContextReducedWithoutCredential(t *testing.T) {
	store := openStore(t)
	rc := loadReliabilityContext(context.Background(), store, "proj-x", "build a time service")
	if !rc.Reduced {
		t.Fatal("no credential → reduced reliability context")
	}
}

// TestLoadReliabilityContextLive (or-xe7.4): with a cached OAuth token pointing at a live MCP
// service, the helper resolves the client from store.Dir()/credentials and pulls live context.
func TestLoadReliabilityContextLive(t *testing.T) {
	store := openStore(t)
	var pid string
	if err := store.WithTx(context.Background(), func(tx *contextstore.Tx) error {
		var e error
		pid, e = tx.Projects().Create(context.Background(), "demo", "build a time service", "http-service")
		return e
	}); err != nil {
		t.Fatal(err)
	}

	// A mock MCP server: initialize + tools/call → a controls payload.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Mcp-Session-Id", "s1")
		if len(req.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		result := map[string]any{"content": []map[string]string{{"type": "text", "text": `[{"code":"RC-012"}]`}}}
		if req.Method == "initialize" {
			result = map[string]any{"serverInfo": map[string]string{"name": "revelara"}}
		}
		b, _ := json.Marshal(result)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": json.RawMessage(b)})
	}))
	defer srv.Close()

	// `orion login` caches the credential here; the token carries the MCP endpoint as its BaseURL.
	ts, err := polaris.NewTokenStore(filepath.Join(store.Dir(), "credentials"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.Save(polaris.Token{AccessToken: "tok", BaseURL: srv.URL}); err != nil {
		t.Fatal(err)
	}

	rc := loadReliabilityContext(context.Background(), store, pid, "cache stampede")
	if rc.Reduced {
		t.Fatal("live MCP token → must NOT be reduced")
	}
	if rc.Sources["controls"] != polaris.SourceLive {
		t.Errorf("controls source = %q, want live", rc.Sources["controls"])
	}
}
