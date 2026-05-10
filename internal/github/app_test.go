package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func generateTestKey(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
}

func TestAppConfigValidate(t *testing.T) {
	cases := []struct {
		name string
		cfg  AppConfig
		ok   bool
	}{
		{"complete", AppConfig{AppID: 1, InstallationID: 2, PrivateKeyPEM: []byte("k")}, true},
		{"no app id", AppConfig{InstallationID: 2, PrivateKeyPEM: []byte("k")}, false},
		{"no install id", AppConfig{AppID: 1, PrivateKeyPEM: []byte("k")}, false},
		{"no key", AppConfig{AppID: 1, InstallationID: 2}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err == nil) != tc.ok {
				t.Errorf("Validate() ok=%v, want %v (err=%v)", err == nil, tc.ok, err)
			}
		})
	}
}

func TestAppJWTHasExpectedClaims(t *testing.T) {
	pemBytes := generateTestKey(t)
	app, err := NewApp(AppConfig{AppID: 12345, InstallationID: 678, PrivateKeyPEM: pemBytes})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	tokStr, err := app.AppJWT(now)
	if err != nil {
		t.Fatalf("AppJWT: %v", err)
	}
	parsed, _, err := new(jwt.Parser).ParseUnverified(tokStr, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("ParseUnverified: %v", err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("claims not MapClaims")
	}
	if claims["iss"] != "12345" {
		t.Errorf("iss = %v, want 12345", claims["iss"])
	}
	iat, _ := claims["iat"].(float64)
	exp, _ := claims["exp"].(float64)
	if iat == 0 || exp == 0 {
		t.Fatalf("missing iat/exp: iat=%v exp=%v", iat, exp)
	}
	if int64(iat) > now.Unix() {
		t.Errorf("iat in future: iat=%d now=%d", int64(iat), now.Unix())
	}
	if exp-iat > 600 {
		t.Errorf("exp-iat > 10min: %f", exp-iat)
	}
}

func TestInstallationTokenExchange(t *testing.T) {
	pemBytes := generateTestKey(t)
	wantToken := "ghs_testtoken123"
	expiry := time.Now().Add(1 * time.Hour).UTC()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		want := "/app/installations/678/access_tokens"
		if r.URL.Path != want {
			t.Errorf("path = %s, want %s", r.URL.Path, want)
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			t.Errorf("Authorization = %q, want Bearer prefix", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      wantToken,
			"expires_at": expiry.Format(time.RFC3339),
		})
	}))
	defer srv.Close()

	app, err := NewApp(AppConfig{
		AppID: 1, InstallationID: 678, PrivateKeyPEM: pemBytes,
		APIBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	tok, err := app.InstallationToken(context.Background())
	if err != nil {
		t.Fatalf("InstallationToken: %v", err)
	}
	if tok != wantToken {
		t.Errorf("token = %q, want %q", tok, wantToken)
	}

	// Cached token should be returned on second call (server should not be hit).
	calls := 0
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	})
	tok2, err := app.InstallationToken(context.Background())
	if err != nil {
		t.Fatalf("InstallationToken cached: %v", err)
	}
	if tok2 != wantToken {
		t.Errorf("cached token mismatch: got %q want %q", tok2, wantToken)
	}
	if calls != 0 {
		t.Errorf("expected 0 calls when cached, got %d", calls)
	}
}

func TestInstallationTokenServerError(t *testing.T) {
	pemBytes := generateTestKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"message":"bad credentials"}`)
	}))
	defer srv.Close()
	app, err := NewApp(AppConfig{AppID: 1, InstallationID: 1, PrivateKeyPEM: pemBytes, APIBaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.InstallationToken(context.Background()); err == nil {
		t.Fatal("expected error on 401, got nil")
	}
}
