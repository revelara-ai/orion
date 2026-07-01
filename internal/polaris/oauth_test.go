package polaris

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// mockWorkOS serves the discovery + token endpoints of a WorkOS-style authorization server (the MCP
// endpoint and the auth server are the same host here). It records the token-endpoint form values.
func mockWorkOS(t *testing.T) (mcpEndpoint string, tokenForms *[]url.Values) {
	t.Helper()
	var forms []url.Values
	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{base}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint": base + "/authorize",
			"token_endpoint":         base + "/token",
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		forms = append(forms, r.Form)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-xyz", "refresh_token": "rt-xyz", "expires_in": 3600})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base = srv.URL
	return srv.URL, &forms
}

// browserThatRedirects returns an OpenBrowser stub simulating WorkOS: it validates the authorize URL
// (PKCE + resource present), then hits the loopback redirect with the given code and (optionally
// tampered) state.
func browserThatRedirects(t *testing.T, mcp, code string, tamperState bool) func(string) error {
	t.Helper()
	return func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
			return fmt.Errorf("authorize URL missing PKCE S256 challenge")
		}
		if q.Get("resource") != mcp {
			return fmt.Errorf("authorize URL missing resource=%s", mcp)
		}
		state := q.Get("state")
		if tamperState {
			state = "wrong-" + state
		}
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?code=" + code + "&state=" + url.QueryEscape(state))
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
}

// TestOAuthAuthorizeFlow (or-xe7.2): the full browser flow — discover → PKCE → loopback → exchange —
// yields the exchanged tokens, and the token request carried the code + a PKCE verifier + resource.
func TestOAuthAuthorizeFlow(t *testing.T) {
	mcp, forms := mockWorkOS(t)
	cfg := OAuthConfig{
		MCPEndpoint: mcp,
		ClientID:    "client-1",
		Scopes:      []string{"reliability:read"},
		OpenBrowser: browserThatRedirects(t, mcp, "mock-code", false),
	}
	tok, err := cfg.Authorize(context.Background())
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if tok.AccessToken != "at-xyz" || tok.RefreshToken != "rt-xyz" {
		t.Errorf("token = %+v, want at-xyz/rt-xyz", tok)
	}
	if tok.ExpiresAt == 0 {
		t.Error("expiry not derived from expires_in")
	}
	if tok.BaseURL != mcp {
		t.Errorf("token BaseURL = %q, want %q", tok.BaseURL, mcp)
	}
	if len(*forms) != 1 {
		t.Fatalf("want 1 token request, got %d", len(*forms))
	}
	f := (*forms)[0]
	if f.Get("grant_type") != "authorization_code" || f.Get("code") != "mock-code" {
		t.Errorf("token form grant/code = %q/%q", f.Get("grant_type"), f.Get("code"))
	}
	if f.Get("code_verifier") == "" {
		t.Error("token request must carry the PKCE code_verifier")
	}
	if f.Get("resource") != mcp {
		t.Error("token request must carry the resource parameter")
	}
}

// TestOAuthRejectsStateMismatch (CSRF defense): a redirect whose state does not match is IGNORED —
// it must never complete the flow, and the forged code must never reach the token endpoint. The
// flow simply keeps waiting (here bounded by a context deadline).
func TestOAuthRejectsStateMismatch(t *testing.T) {
	mcp, forms := mockWorkOS(t)
	cfg := OAuthConfig{MCPEndpoint: mcp, ClientID: "c", OpenBrowser: browserThatRedirects(t, mcp, "forged-code", true)}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := cfg.Authorize(ctx); err == nil {
		t.Fatal("a forged wrong-state callback must NOT complete authorization")
	}
	if len(*forms) != 0 {
		t.Errorf("a forged wrong-state code must never be exchanged at the token endpoint, got %d exchanges", len(*forms))
	}
}

// TestOAuthRefresh: a refresh-token exchange rediscovers the endpoint and returns a fresh token.
func TestOAuthRefresh(t *testing.T) {
	mcp, forms := mockWorkOS(t)
	tok, err := OAuthConfig{MCPEndpoint: mcp, ClientID: "c"}.Refresh(context.Background(), "rt-old")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if tok.AccessToken != "at-xyz" {
		t.Errorf("refreshed access token = %q", tok.AccessToken)
	}
	if f := (*forms)[0]; f.Get("grant_type") != "refresh_token" || f.Get("refresh_token") != "rt-old" {
		t.Errorf("refresh form = %v", f)
	}
}

// TestOAuthRejectsForeignAuthServer (security, or-xe7.2): if the MCP metadata names an untrusted
// authorization server, discovery refuses BEFORE any code/verifier could be sent there (mix-up /
// token-exfiltration defense) — it never even fetches the foreign host.
func TestOAuthRejectsForeignAuthServer(t *testing.T) {
	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, _ *http.Request) {
		// Point at an attacker-controlled auth server on a foreign host.
		_ = json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{"https://evil.example"}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	base = srv.URL

	cfg := OAuthConfig{MCPEndpoint: base, ClientID: "c", OpenBrowser: func(string) error { return nil }}
	_, err := cfg.Authorize(context.Background())
	if err == nil || !containsAny(err.Error(), "untrusted") {
		t.Fatalf("must refuse an untrusted authorization server, got err=%v", err)
	}
}

// TestOAuthErrorPathIgnoresStrayCallback (security, or-xe7.2): a ?error= callback carrying the WRONG
// state must NOT abort the pending login — only the correctly-stated success callback proceeds.
func TestOAuthErrorPathIgnoresStrayCallback(t *testing.T) {
	mcp, _ := mockWorkOS(t)
	cfg := OAuthConfig{
		MCPEndpoint: mcp,
		ClientID:    "c",
		OpenBrowser: func(authURL string) error {
			u, _ := url.Parse(authURL)
			redirect := u.Query().Get("redirect_uri")
			state := u.Query().Get("state")
			// A stray/forged abort with the wrong state — must be ignored.
			if resp, err := http.Get(redirect + "?error=access_denied&state=WRONG"); err == nil {
				_ = resp.Body.Close()
			}
			// The genuine, correctly-stated success callback.
			go func() {
				if resp, err := http.Get(redirect + "?code=real-code&state=" + url.QueryEscape(state)); err == nil {
					_ = resp.Body.Close()
				}
			}()
			return nil
		},
	}
	tok, err := cfg.Authorize(context.Background())
	if err != nil {
		t.Fatalf("a stray wrong-state ?error= must not abort the flow: %v", err)
	}
	if tok.AccessToken != "at-xyz" {
		t.Errorf("flow should have completed with the real code, got %+v", tok)
	}
}

func containsAny(s, sub string) bool { return len(sub) > 0 && strings.Contains(s, sub) }

// TestPKCEChallengeIsS256: the challenge is the base64url(sha256(verifier)).
func TestPKCEChallengeIsS256(t *testing.T) {
	v, ch := pkce()
	sum := sha256.Sum256([]byte(v))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); ch != want {
		t.Errorf("challenge = %q, want S256 %q", ch, want)
	}
}
