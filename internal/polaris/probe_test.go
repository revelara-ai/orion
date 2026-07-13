package polaris

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeMCP serves initialize + tools/list; mode selects the failure shape.
func fakeMCP(t *testing.T, mode string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "no-org":
			http.Error(w, `{"error":"no organization context"}`, http.StatusForbidden)
			return
		case "denied":
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		case "unauthorized":
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		body, _ := json.RawMessage(nil), json.NewDecoder(r.Body).Decode(&req)
		_ = body
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%v,"result":{"serverInfo":{"name":"fake","version":"1"},"protocolVersion":"2025-03-26","capabilities":{}}}`, jsonID(req.ID))
		case "tools/list":
			if mode == "zero-tools" {
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%v,"result":{"tools":[]}}`, jsonID(req.ID))
			} else {
				fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%v,"result":{"tools":[{"name":"search_incidents"},{"name":"search_risks"}]}}`, jsonID(req.ID))
			}
		default:
			w.WriteHeader(202)
		}
	}))
}

func jsonID(id any) string {
	b, _ := json.Marshal(id)
	return string(b)
}

// The or-xe7.9 probe contract: a healthy session reports its tool count; an
// org-less JWT (403 + "organization") teaches the fix at LOGIN time instead
// of surfacing later as silently-reduced context.
func TestProbeSessionClassifies(t *testing.T) {
	cases := []struct {
		mode    string
		wantErr string // "" = success
		wantOK  string
	}{
		{mode: "ok", wantOK: "2 tools available"},
		{mode: "no-org", wantErr: "NO ORGANIZATION CONTEXT"},
		{mode: "denied", wantErr: "organization or tool grant"},
		{mode: "unauthorized", wantErr: "run /mcp login again"},
		{mode: "zero-tools", wantErr: "exposes no tools"},
	}
	for _, tc := range cases {
		srv := fakeMCP(t, tc.mode)
		detail, err := ProbeSession(context.Background(), srv.URL, Token{AccessToken: "tok"})
		srv.Close()
		if tc.wantErr == "" {
			if err != nil || !strings.Contains(detail, tc.wantOK) {
				t.Fatalf("%s: want %q, got detail=%q err=%v", tc.mode, tc.wantOK, detail, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s: want error containing %q, got %v", tc.mode, tc.wantErr, err)
		}
	}
}

// or-xe7.9 single-flight: WorkOS refresh tokens are single-use — N concurrent
// EnsureFreshToken calls must produce EXACTLY ONE refresh grant; the waiters
// adopt the winner's persisted rotation instead of burning the old token.
func TestEnsureFreshTokenSingleFlight(t *testing.T) {
	var grants atomic.Int64
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "oauth-protected-resource"):
			fmt.Fprintf(w, `{"authorization_servers":[%q]}`, srvURL)
		case strings.Contains(r.URL.Path, "oauth-authorization-server"):
			fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q}`, srvURL, srvURL+"/authorize", srvURL+"/oauth2/token")
		case r.URL.Path == "/oauth2/token":
			grants.Add(1)
			fmt.Fprintf(w, `{"access_token":"fresh-%d","refresh_token":"rot-%d","expires_in":300}`, grants.Load(), grants.Load())
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	ts, err := NewTokenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	expired := Token{
		AccessToken: "old", RefreshToken: "single-use", ClientID: "client",
		ExpiresAt: time.Now().Add(-time.Hour).Unix(),
	}
	if err := ts.Save(expired); err != nil {
		t.Fatal(err)
	}

	const n = 6
	type out struct {
		tok Token
		err error
	}
	results := make(chan out, n)
	for i := 0; i < n; i++ {
		go func() {
			tok, _, err := EnsureFreshToken(context.Background(), ts, srvURL+"/mcp", time.Now())
			results <- out{tok, err}
		}()
	}
	for i := 0; i < n; i++ {
		r := <-results
		if r.err != nil {
			t.Fatalf("concurrent refresh errored: %v", r.err)
		}
		if !strings.HasPrefix(r.tok.AccessToken, "fresh-") {
			t.Fatalf("caller did not receive the refreshed token: %q", r.tok.AccessToken)
		}
	}
	if g := grants.Load(); g != 1 {
		t.Fatalf("refresh grant must be single-flight: %d grants for %d callers", g, n)
	}
}
