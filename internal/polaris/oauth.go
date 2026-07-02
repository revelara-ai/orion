package polaris

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// OAuthConfig drives the WorkOS AuthKit browser flow that authorizes Orion to the revelara.ai MCP
// service (or-xe7.2). It is OAuth 2.1: authorization-code + PKCE (S256), with the authorization and
// token endpoints DISCOVERED from the MCP resource server — so no WorkOS specifics are hardcoded.
type OAuthConfig struct {
	MCPEndpoint      string             // the protected resource; also the RFC 8707 `resource` parameter
	ClientID         string             // the WorkOS OAuth client id
	Scopes           []string           // requested scopes (optional)
	HTTP             *http.Client       // optional; defaults to a 15s-timeout client
	OpenBrowser      func(string) error // optional; defaults to opening the OS browser. Stubbed in tests.
	AllowedAuthHosts []string           // extra trusted authorization-server hosts/suffixes (beyond the MCP domain + WorkOS)
}

// authServer is the subset of the authorization-server metadata Orion needs.
type authServer struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint"` // RFC 7591 dynamic client registration (optional)
}

// scopeList is the requested OAuth scopes, defaulting to the AuthKit MCP scopes when none are set:
// openid + offline_access (offline_access is what buys a refresh token).
func (cfg OAuthConfig) scopeList() []string {
	if len(cfg.Scopes) > 0 {
		return cfg.Scopes
	}
	return []string{"openid", "offline_access"}
}

// Authorize runs the full browser flow and returns a cached-ready Token. It: discovers the auth
// server from the MCP endpoint, generates a PKCE pair, starts a loopback listener, opens the
// browser to the authorization URL, waits for the redirect carrying the code (validating state),
// and exchanges the code (+ verifier) for tokens.
func (cfg OAuthConfig) Authorize(ctx context.Context) (Token, error) {
	httpc := cfg.HTTP
	if httpc == nil {
		httpc = &http.Client{Timeout: 15 * time.Second}
	}
	as, err := discover(ctx, httpc, cfg.MCPEndpoint, cfg.AllowedAuthHosts)
	if err != nil {
		return Token{}, err
	}

	verifier, challenge := pkce()
	state, err := randToken()
	if err != nil {
		return Token{}, err
	}

	// Loopback redirect target (127.0.0.1 only — never a routable interface).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return Token{}, fmt.Errorf("oauth loopback: %w", err)
	}
	defer func() { _ = ln.Close() }()
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)

	// Resolve the client id: a pre-provisioned ORION_WORKOS_CLIENT_ID, else RFC 7591 dynamic client
	// registration against the AS (the MCP-client pattern — no pre-registered client needed).
	clientID, err := cfg.resolveClientID(ctx, httpc, as, redirectURI)
	if err != nil {
		return Token{}, err
	}

	type result struct {
		code string
		err  error
	}
	resc := make(chan result, 1)
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		// Validate state FIRST, on EVERY path: a forged/stray callback — including one carrying
		// ?error= — with the wrong (or no) state is IGNORED, never aborting the pending flow. This
		// closes both the CSRF code-injection and the login-abort vectors a local process could fire.
		if q.Get("state") != state {
			http.Error(w, "unexpected callback", http.StatusBadRequest)
			return
		}
		if e := q.Get("error"); e != "" {
			_, _ = io.WriteString(w, "Authorization failed. You can close this window.")
			resc <- result{err: fmt.Errorf("oauth: authorization denied: %s %s", e, q.Get("error_description"))}
			return
		}
		_, _ = io.WriteString(w, "Authorized. You can close this window and return to Orion.")
		resc <- result{code: q.Get("code")}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	authURL := authorizeURL(as.AuthorizationEndpoint, clientID, cfg.MCPEndpoint, redirectURI, state, challenge, cfg.scopeList())
	open := cfg.OpenBrowser
	if open == nil {
		open = defaultOpenBrowser
	}
	if err := open(authURL); err != nil {
		return Token{}, fmt.Errorf("oauth: open browser: %w", err)
	}

	select {
	case <-ctx.Done():
		return Token{}, ctx.Err()
	case res := <-resc:
		if res.err != nil {
			return Token{}, res.err
		}
		if res.code == "" {
			return Token{}, fmt.Errorf("oauth: no authorization code in redirect")
		}
		return cfg.exchange(ctx, httpc, as.TokenEndpoint, url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {res.code},
			"redirect_uri":  {redirectURI},
			"client_id":     {clientID},
			"code_verifier": {verifier},
			"resource":      {cfg.MCPEndpoint},
		})
	}
}

// Refresh exchanges a refresh token for a fresh access token, rediscovering the token endpoint.
func (cfg OAuthConfig) Refresh(ctx context.Context, refreshToken string) (Token, error) {
	httpc := cfg.HTTP
	if httpc == nil {
		httpc = &http.Client{Timeout: 15 * time.Second}
	}
	as, err := discover(ctx, httpc, cfg.MCPEndpoint, cfg.AllowedAuthHosts)
	if err != nil {
		return Token{}, err
	}
	return cfg.exchange(ctx, httpc, as.TokenEndpoint, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {cfg.ClientID},
		"resource":      {cfg.MCPEndpoint},
	})
}

// exchange POSTs the token request and maps the response onto a Token.
func (cfg OAuthConfig) exchange(ctx context.Context, httpc *http.Client, tokenEndpoint string, form url.Values) (Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpc.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("oauth token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return Token{}, fmt.Errorf("oauth token exchange: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return Token{}, fmt.Errorf("oauth token exchange: decode: %w", err)
	}
	if out.AccessToken == "" {
		return Token{}, fmt.Errorf("oauth token exchange: no access_token in response")
	}
	tok := Token{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken, BaseURL: cfg.MCPEndpoint}
	if out.ExpiresIn > 0 {
		tok.ExpiresAt = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second).Unix()
	}
	return tok, nil
}

// resolveClientID returns the pre-provisioned client id, or dynamically registers one (RFC 7591)
// with the authorization server when none is configured and the server advertises a registration
// endpoint — so Orion connects as an MCP client without a pre-registered WorkOS client (the CIMD/DCR
// path the polaris MCP setup expects).
func (cfg OAuthConfig) resolveClientID(ctx context.Context, httpc *http.Client, as authServer, redirectURI string) (string, error) {
	if cfg.ClientID != "" {
		return cfg.ClientID, nil
	}
	if as.RegistrationEndpoint == "" {
		return "", fmt.Errorf("oauth: no client id set and the authorization server offers no dynamic registration — set ORION_WORKOS_CLIENT_ID")
	}
	return cfg.registerClient(ctx, httpc, as.RegistrationEndpoint, redirectURI)
}

// registerClient performs RFC 7591 dynamic client registration for a public, loopback-redirect
// native client (PKCE, no client secret) and returns the issued client_id. The registration
// endpoint must be https and a trusted host — the same trust boundary discovery enforces.
func (cfg OAuthConfig) registerClient(ctx context.Context, httpc *http.Client, regEndpoint, redirectURI string) (string, error) {
	regU, err := requireSecureURL(regEndpoint)
	if err != nil {
		return "", fmt.Errorf("oauth register: %w", err)
	}
	mcpU, err := requireSecureURL(cfg.MCPEndpoint)
	if err != nil || !authHostAllowed(regU.Hostname(), mcpU.Hostname(), cfg.AllowedAuthHosts) {
		return "", fmt.Errorf("oauth register: untrusted registration host %q", regU.Hostname())
	}
	body, _ := json.Marshal(map[string]any{
		"client_name":                "Orion",
		"application_type":           "native",
		"token_endpoint_auth_method": "none", // public client — PKCE, no secret
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"redirect_uris":              []string{redirectURI},
		"scope":                      strings.Join(cfg.scopeList(), " "),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, regEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := httpc.Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth register: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("oauth register: status %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	var out struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(rb, &out); err != nil {
		return "", fmt.Errorf("oauth register: decode: %w", err)
	}
	if out.ClientID == "" {
		return "", fmt.Errorf("oauth register: no client_id in response")
	}
	return out.ClientID, nil
}

// discover resolves the authorization + token endpoints from the MCP resource server's
// protected-resource metadata (RFC 9728) → the authorization-server metadata (RFC 8414). It is the
// trust boundary: the code + PKCE verifier are later POSTed to the token endpoint this returns, so
// (1) every URL must be https (loopback exempt for tests/local dev), (2) the authorization server
// and its endpoints must be a TRUSTED host — the MCP endpoint's own domain, a WorkOS host, or an
// explicit allow entry — else a compromised/MITM'd MCP metadata could exfiltrate the token to an
// attacker's endpoint (RFC 8414 mix-up), and (3) the AS issuer must match the advertised server.
func discover(ctx context.Context, httpc *http.Client, mcpEndpoint string, allow []string) (authServer, error) {
	mcpU, err := requireSecureURL(mcpEndpoint)
	if err != nil {
		return authServer{}, fmt.Errorf("oauth discover: MCP endpoint %w", err)
	}
	var prm struct {
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := getJSON(ctx, httpc, wellKnown(mcpEndpoint, "oauth-protected-resource"), &prm); err != nil {
		return authServer{}, fmt.Errorf("oauth discover (protected-resource): %w", err)
	}
	if len(prm.AuthorizationServers) == 0 {
		return authServer{}, fmt.Errorf("oauth discover: MCP server advertises no authorization_servers")
	}
	asIssuer := prm.AuthorizationServers[0]
	asU, err := requireSecureURL(asIssuer)
	if err != nil {
		return authServer{}, fmt.Errorf("oauth discover: authorization server %w", err)
	}
	if !authHostAllowed(asU.Hostname(), mcpU.Hostname(), allow) {
		return authServer{}, fmt.Errorf("oauth discover: untrusted authorization server %q (not the MCP domain, a WorkOS host, or an allowed host)", asU.Hostname())
	}
	var as authServer
	if err := getJSON(ctx, httpc, wellKnown(asIssuer, "oauth-authorization-server"), &as); err != nil {
		return authServer{}, fmt.Errorf("oauth discover (authorization-server): %w", err)
	}
	if as.AuthorizationEndpoint == "" || as.TokenEndpoint == "" {
		return authServer{}, fmt.Errorf("oauth discover: authorization server missing endpoints")
	}
	if as.Issuer != "" && strings.TrimRight(as.Issuer, "/") != strings.TrimRight(asIssuer, "/") {
		return authServer{}, fmt.Errorf("oauth discover: issuer %q does not match advertised server %q", as.Issuer, asIssuer)
	}
	// The endpoints the flow actually POSTs to must themselves be https + a trusted host.
	for _, ep := range []string{as.AuthorizationEndpoint, as.TokenEndpoint} {
		epU, err := requireSecureURL(ep)
		if err != nil {
			return authServer{}, fmt.Errorf("oauth discover: endpoint %w", err)
		}
		if !authHostAllowed(epU.Hostname(), mcpU.Hostname(), allow) {
			return authServer{}, fmt.Errorf("oauth discover: untrusted endpoint host %q", epU.Hostname())
		}
	}
	return as, nil
}

// requireSecureURL parses raw and requires https, except for loopback hosts (127.0.0.1/::1/localhost)
// so tests and local dev can use http.
func requireSecureURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid URL %q", raw)
	}
	if u.Scheme != "https" && !isLoopbackHost(u.Hostname()) {
		return nil, fmt.Errorf("insecure (non-https) URL %q", raw)
	}
	return u, nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// authHostAllowed reports whether host is a trusted authorization-server host: the MCP endpoint's
// own host, a host sharing the MCP endpoint's registrable domain, a built-in WorkOS host, or an
// explicit allow entry. Loopback is always trusted (tests/local dev).
func authHostAllowed(host, mcpHost string, allow []string) bool {
	host = strings.ToLower(host)
	mcpHost = strings.ToLower(mcpHost)
	if host == mcpHost || isLoopbackHost(host) {
		return true
	}
	if registrableDomain(host) == registrableDomain(mcpHost) && registrableDomain(host) != "" {
		return true
	}
	for _, t := range append([]string{"workos.com", "authkit.app"}, allow...) {
		t = strings.ToLower(strings.TrimPrefix(t, "."))
		if host == t || strings.HasSuffix(host, "."+t) {
			return true
		}
	}
	return false
}

// registrableDomain is a naive registrable-domain (last two dot-labels). Sufficient for the
// single-tenant revelara.ai deployment; the WorkOS + explicit allowlist covers cross-domain auth.
func registrableDomain(host string) string {
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return ""
	}
	return strings.Join(labels[len(labels)-2:], ".")
}

// wellKnown builds a `<origin>/.well-known/<name>` URL, preserving the origin (scheme+host) and
// dropping any path on the input.
func wellKnown(raw, name string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.TrimRight(raw, "/") + "/.well-known/" + name
	}
	return u.Scheme + "://" + u.Host + "/.well-known/" + name
}

func getJSON(ctx context.Context, httpc *http.Client, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", u, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// authorizeURL builds the OAuth authorization request URL.
func authorizeURL(endpoint, clientID, resource, redirectURI, state, challenge string, scopes []string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"resource":              {resource},
	}
	if len(scopes) > 0 {
		q.Set("scope", strings.Join(scopes, " "))
	}
	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	return endpoint + sep + q.Encode()
}

// pkce returns a PKCE (verifier, S256 challenge) pair.
func pkce() (verifier, challenge string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge
}

func randToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// defaultOpenBrowser opens url in the OS browser.
func defaultOpenBrowser(u string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{u}
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", u}
	default:
		cmd, args = "xdg-open", []string{u}
	}
	// #nosec G204 -- cmd is a fixed OS-browser launcher (open/rundll32/xdg-open); args is Orion's own
	// authorization URL, and exec.Command invokes no shell — there is no injection surface.
	return exec.Command(cmd, args...).Start()
}
