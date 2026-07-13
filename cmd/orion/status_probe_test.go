package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/revelara-ai/orion/internal/health"
	"github.com/revelara-ai/orion/internal/polaris"
)

// or-xe7.5: the status probe verifies the OAuth token against the MCP service
// (JSON-RPC initialize), NOT the retired REST /auth/me — proven by a fake MCP
// server that records the method it was called with.
func TestLivePolarisCheckUsesMCPInitialize(t *testing.T) {
	var sawInitialize atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/auth/me") {
			t.Error("status probe hit the RETIRED REST /auth/me endpoint")
		}
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Method == "initialize" {
			sawInitialize.Store(true)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"serverInfo":{"name":"revelara","version":"1"}}}`, jsonID(req.ID))
			return
		}
		w.WriteHeader(202) // the notifications/initialized follow-up
	}))
	defer srv.Close()

	dir := t.TempDir()
	t.Setenv("ORION_DATA_DIR", dir)
	t.Setenv("ORION_POLARIS_MCP_URL", srv.URL)
	ts, err := polaris.NewTokenStore(filepath.Join(dir, "credentials"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.Save(polaris.Token{AccessToken: "tok", BaseURL: srv.URL}); err != nil {
		t.Fatal(err)
	}

	c := livePolarisCheck()
	if !sawInitialize.Load() {
		t.Fatal("status probe never called MCP initialize")
	}
	if c.Status != health.OK || !strings.Contains(c.Detail, "MCP session verified") {
		t.Fatalf("a valid MCP session must report OK: %+v", c)
	}
}

func jsonID(id any) string {
	b, _ := json.Marshal(id)
	return string(b)
}

// The failure half: a cached token whose MCP session fails to initialize must
// WARN, not falsely report OK (the retired REST Me() couldn't attest this).
func TestLivePolarisCheckWarnsOnMCPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	dir := t.TempDir()
	t.Setenv("ORION_DATA_DIR", dir)
	t.Setenv("ORION_POLARIS_MCP_URL", srv.URL)
	ts, _ := polaris.NewTokenStore(filepath.Join(dir, "credentials"))
	if err := ts.Save(polaris.Token{AccessToken: "stale", BaseURL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	c := livePolarisCheck()
	if c.Status != health.Warn || !strings.Contains(c.Detail, "MCP unreachable") {
		t.Fatalf("a failed MCP session must WARN, not falsely OK: %+v", c)
	}
}
