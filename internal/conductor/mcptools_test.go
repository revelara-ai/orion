package conductor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/polaris"
	"github.com/revelara-ai/orion/internal/tools"
)

// TestRegisterMCPToolsExposesRemoteTools (or-xe7.10): when authenticated, each revelara.ai MCP tool
// is registered as a Conductor tool (prefixed, read-only) whose Run proxies tools/call.
func TestRegisterMCPToolsExposesRemoteTools(t *testing.T) {
	t.Setenv("ORION_POLARIS_MCP_URL", "") // let the token's BaseURL drive the endpoint
	store := openStore(t)

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
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"serverInfo": map[string]string{"name": "polaris"}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{"name": "search_controls", "description": "search reliability controls"}}}
		case "tools/call":
			result = map[string]any{"content": []map[string]string{{"type": "text", "text": `[{"code":"RC-012"}]`}}}
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
			return
		}
		b, _ := json.Marshal(result)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": json.RawMessage(b)})
	}))
	defer srv.Close()

	ts, err := polaris.NewTokenStore(filepath.Join(store.Dir(), "credentials"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.Save(polaris.Token{AccessToken: "tok", BaseURL: srv.URL}); err != nil {
		t.Fatal(err)
	}

	r := tools.NewRegistry()
	registerMCPTools(r, store)

	tool, ok := r.Get("revelara_search_controls")
	if !ok {
		t.Fatal("revelara_search_controls should be registered")
	}
	if !strings.Contains(tool.Description, "revelara.ai") {
		t.Errorf("description should mark provenance, got %q", tool.Description)
	}
	if !tool.Safety.ReadOnly {
		t.Error("revelara.ai research tools should be ReadOnly")
	}
	out, err := tool.Run(context.Background(), json.RawMessage(`{"query":"cache stampede"}`))
	if err != nil {
		t.Fatalf("tool run: %v", err)
	}
	if !strings.Contains(out, "RC-012") {
		t.Errorf("Run should proxy the MCP tools/call result, got %q", out)
	}
}

// TestRegisterMCPToolsNoCredential (or-xe7.10): with no cached credential, no revelara.ai tools are
// registered — the agent runs without the surface.
func TestRegisterMCPToolsNoCredential(t *testing.T) {
	store := openStore(t)
	r := tools.NewRegistry()
	registerMCPTools(r, store)
	if _, ok := r.Get("revelara_search_controls"); ok {
		t.Error("no credential → no revelara.ai tools should be registered")
	}
}
