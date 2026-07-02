package polaris

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

func openStore(t *testing.T) *contextstore.Store {
	t.Helper()
	s, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func seedProject(t *testing.T, s *contextstore.Store) string {
	t.Helper()
	ctx := context.Background()
	var pid string
	_ = s.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		pid, e = tx.Projects().Create(ctx, "demo", "build a time service", "http-service")
		return e
	})
	return pid
}

// mcpToolServer serves the MCP subset the Consumer uses (initialize + tools/call), returning the
// JSON text configured per tool name. The caller owns Close (so tests can simulate going offline).
func mcpToolServer(t *testing.T, byTool map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Mcp-Session-Id", "s1")
		if len(req.ID) == 0 { // notification (notifications/initialized)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		reply := func(result any) {
			b, _ := json.Marshal(result)
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": json.RawMessage(b)})
		}
		switch req.Method {
		case "initialize":
			reply(map[string]any{"serverInfo": map[string]string{"name": "revelara"}})
		case "tools/call":
			text := byTool[req.Params.Name]
			if text == "" {
				text = "[]"
			}
			reply(map[string]any{"content": []map[string]string{{"type": "text", "text": text}}})
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
		}
	}))
}

// TestLoopProceedsWhenMCPUnreachable: with the MCP service unreachable and no cache, Load returns no
// error, flags reduced context, and yields empty (not nil) data — the loop proceeds.
func TestLoopProceedsWhenMCPUnreachable(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	pid := seedProject(t, s)

	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	u := closed.URL
	closed.Close() // connection refused → unreachable

	consumer := NewConsumer(NewMCPClient(u, "tok"), s)
	rc, err := consumer.Load(ctx, pid, "time service")
	if err != nil {
		t.Fatalf("Load must not error when the MCP service is unreachable: %v", err)
	}
	if !rc.Reduced {
		t.Fatal("expected Reduced=true when the MCP service is unreachable")
	}
	if rc.Sources["controls"] != SourceEmpty {
		t.Fatalf("controls source = %q, want empty", rc.Sources["controls"])
	}
	if string(rc.Controls) != "[]" {
		t.Fatalf("controls = %q, want []", rc.Controls)
	}
}

// TestCacheHitWorksOffline: a live tool call caches; a later unreachable call serves the cached
// payload and flags reduced context.
func TestCacheHitWorksOffline(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	pid := seedProject(t, s)

	srv := mcpToolServer(t, map[string]string{"search_controls": `[{"code":"C1","title":"timeouts"}]`})

	// Live load caches all three kinds.
	live := NewConsumer(NewMCPClient(srv.URL, "tok"), s)
	rc, err := live.Load(ctx, pid, "time service")
	if err != nil {
		t.Fatalf("live load: %v", err)
	}
	if rc.Reduced || rc.Sources["controls"] != SourceLive {
		t.Fatalf("expected live, non-reduced; got reduced=%v sources=%v", rc.Reduced, rc.Sources)
	}
	offlineURL := srv.URL
	srv.Close() // now unreachable

	// Offline load falls back to cache.
	offline := NewConsumer(NewMCPClient(offlineURL, "tok"), s)
	rc2, err := offline.Load(ctx, pid, "time service")
	if err != nil {
		t.Fatalf("offline load: %v", err)
	}
	if !rc2.Reduced {
		t.Fatal("offline load should flag reduced context")
	}
	if rc2.Sources["controls"] != SourceCache {
		t.Fatalf("controls source = %q, want cache", rc2.Sources["controls"])
	}
	if !strings.Contains(string(rc2.Controls), "timeouts") {
		t.Fatalf("cached controls not served offline: %s", rc2.Controls)
	}
}
