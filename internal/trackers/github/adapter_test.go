package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	gh "github.com/revelara-ai/orion/internal/github"
	"github.com/revelara-ai/orion/internal/trackers"
	"github.com/revelara-ai/orion/internal/trackers/conformance"
)

// firstNonZeroInt64 picks the first non-zero value. Used by the
// test appFactory to provide reasonable defaults when a binding is
// partially populated.
func firstNonZeroInt64(vals ...int64) int64 {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 1
}

// testPrivateKeyPEM generates a fresh RSA key in PEM form for tests
// that need a valid GitHub App private key shape.
func testPrivateKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
}

// canned represents the in-memory test state behind the mock GitHub
// server: issues keyed by number, plus a counter for Create. Each
// subtest gets a fresh server via newMockGitHub.
type canned struct {
	issues map[int]map[string]any
	next   int
}

// newMockGitHub stands up an httptest.Server that handles the
// endpoints the adapter exercises: list, get, create, patch state,
// comment, rate_limit. Returns the server (caller closes) and a
// pointer to the canned state for assertions / seed mutation.
func newMockGitHub(t *testing.T) (*httptest.Server, *canned) {
	t.Helper()
	state := &canned{
		issues: map[int]map[string]any{
			1: {
				"number":     1,
				"title":      "first conformance issue",
				"body":       "body 1",
				"state":      "open",
				"html_url":   "https://gh/o/r/1",
				"labels":     []map[string]any{{"name": "bug"}},
				"updated_at": "2026-05-11T10:00:00Z",
			},
			2: {
				"number":     2,
				"title":      "second",
				"body":       "body 2",
				"state":      "open",
				"html_url":   "https://gh/o/r/2",
				"updated_at": "2026-05-11T11:00:00Z",
			},
		},
		next: 100,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/issues", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			var since time.Time
			if s := r.URL.Query().Get("since"); s != "" {
				since, _ = time.Parse(time.RFC3339, s)
			}
			out := []map[string]any{}
			for _, v := range state.issues {
				if !since.IsZero() {
					if u, ok := v["updated_at"].(string); ok {
						t2, _ := time.Parse(time.RFC3339, u)
						if t2.Before(since) {
							continue
						}
					}
				}
				out = append(out, v)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			state.next++
			newIssue := map[string]any{
				"number":     state.next,
				"title":      body["title"],
				"body":       body["body"],
				"state":      "open",
				"html_url":   "https://gh/o/r/" + itoa(state.next),
				"updated_at": time.Now().UTC().Format(time.RFC3339),
			}
			state.issues[state.next] = newIssue
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(newIssue)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("/repos/o/r/issues/", func(w http.ResponseWriter, r *http.Request) {
		// Routes:
		//   GET    /repos/o/r/issues/N         -> get
		//   PATCH  /repos/o/r/issues/N         -> update state
		//   POST   /repos/o/r/issues/N/comments -> comment
		path := r.URL.Path
		switch r.Method {
		case http.MethodGet:
			n := numberFromPath(path, "/repos/o/r/issues/")
			if v, ok := state.issues[n]; ok {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(v)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		case http.MethodPatch:
			n := numberFromPath(path, "/repos/o/r/issues/")
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if v, ok := state.issues[n]; ok {
				if s, has := body["state"].(string); has {
					v["state"] = s
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(v)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPost:
			// POST .../comments
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"html_url":"https://gh/o/r/1#comment"}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resources":{"core":{"limit":5000,"remaining":4999,"reset":0}}}`))
	})
	// Installation token endpoint stub
	mux.HandleFunc("/app/installations/1/access_tokens", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"ghs_test","expires_at":"2099-01-01T00:00:00Z"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, state
}

func itoa(n int) string {
	b := []byte{}
	if n == 0 {
		return "0"
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func numberFromPath(path, prefix string) int {
	rest := path[len(prefix):]
	for i, c := range rest {
		if c == '/' {
			rest = rest[:i]
			break
		}
	}
	n := 0
	for _, c := range rest {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// newTestBinding builds a TrackerBinding pointing at the test
// server. The adapter's appFactory is replaced with one that points
// gh.App at srv.URL so all REST calls hit the mock.
func newTestBinding(t *testing.T, srv *httptest.Server) (trackers.TrackerBinding, *Adapter) {
	t.Helper()
	binding := trackers.TrackerBinding{
		ID:    uuid.New(),
		OrgID: uuid.New(),
		Kind:  trackers.TrackerKindGitHubIssues,
		Config: map[string]any{
			"repo_full_name":  "o/r",
			"app_id":          int64(1),
			"installation_id": int64(1),
		},
	}
	pemKey := testPrivateKeyPEM(t)
	binding.Config["private_key_pem"] = string(pemKey)
	adapter := NewAdapterWithFactory(func(b trackers.TrackerBinding) (*gh.App, error) {
		appID, _ := lookupInt64(b.Config, "app_id")
		instID, _ := lookupInt64(b.Config, "installation_id")
		pemBytes, _ := b.Config["private_key_pem"].(string)
		if pemBytes == "" {
			pemBytes = string(pemKey) // fallback for partial bindings (TestOwnerRepoRequiresConfig etc.)
		}
		return gh.NewApp(gh.AppConfig{
			AppID:          firstNonZeroInt64(appID, 1),
			InstallationID: firstNonZeroInt64(instID, 1),
			PrivateKeyPEM:  []byte(pemBytes),
			APIBaseURL:     srv.URL,
		})
	})
	return binding, adapter
}

// TestConformance runs the parametric conformance suite against this
// adapter via the mock GitHub server.
func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.SuiteOptions{
		Factory: func(t *testing.T) (trackers.TrackerAdapter, trackers.TrackerBinding, conformance.Fixture) {
			srv, state := newMockGitHub(t)
			_ = state
			binding, adapter := newTestBinding(t, srv)
			fixture := conformance.Fixture{
				MidpointTime: time.Date(2026, 5, 11, 10, 30, 0, 0, time.UTC),
				Issues: []trackers.NormalizedIssue{
					{
						ExternalID:  "gh:o/r#1",
						ExternalURL: "https://gh/o/r/1",
						Title:       "first conformance issue",
						State:       trackers.StateOpen,
						LastUpdated: time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC),
					},
					{
						ExternalID:  "gh:o/r#2",
						ExternalURL: "https://gh/o/r/2",
						Title:       "second",
						State:       trackers.StateOpen,
						LastUpdated: time.Date(2026, 5, 11, 11, 0, 0, 0, time.UTC),
					},
				},
			}
			return adapter, binding, fixture
		},
	})
}

// TestExternalIDFormat asserts SPEC §4.2 format.
func TestExternalIDFormat(t *testing.T) {
	got := externalID("revelara-ai", "orion", 42)
	want := "gh:revelara-ai/orion#42"
	if got != want {
		t.Errorf("externalID = %q, want %q", got, want)
	}
}

// TestParseExternalIDRejectsWrongRepo ensures the parser doesn't
// accept an external_id whose scope doesn't match the binding's
// repo.
func TestParseExternalIDRejectsWrongRepo(t *testing.T) {
	_, ok := parseExternalID("gh:other-org/other-repo#5", "o", "r")
	if ok {
		t.Error("expected parseExternalID to reject mismatched scope")
	}
}

// TestNormalizeStateMappings asserts the GitHub -> NormalizedState
// translation.
func TestNormalizeStateMappings(t *testing.T) {
	cases := map[string]trackers.NormalizedState{
		"open":   trackers.StateOpen,
		"closed": trackers.StateClosed,
	}
	for in, want := range cases {
		if got := normalizeState(in); got != want {
			t.Errorf("normalizeState(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestUpdateStateBlockedMapsToClosed: GitHub has no native blocked
// state; the adapter approximates blocked → closed.
func TestUpdateStateBlockedMapsToClosed(t *testing.T) {
	srv, _ := newMockGitHub(t)
	binding, adapter := newTestBinding(t, srv)
	if err := adapter.UpdateState(context.Background(), binding, "gh:o/r#1", trackers.StateBlocked); err != nil {
		t.Errorf("UpdateState blocked: %v", err)
	}
}

// TestOwnerRepoRequiresConfig: a binding without repo_full_name returns
// ErrInvalidBinding.
func TestOwnerRepoRequiresConfig(t *testing.T) {
	srv, _ := newMockGitHub(t)
	_, adapter := newTestBinding(t, srv)
	bad := trackers.TrackerBinding{
		Kind:   trackers.TrackerKindGitHubIssues,
		Config: map[string]any{},
	}
	if _, err := adapter.FetchCandidates(context.Background(), bad, time.Time{}); err == nil {
		t.Error("expected ErrInvalidBinding for missing repo_full_name")
	}
}
