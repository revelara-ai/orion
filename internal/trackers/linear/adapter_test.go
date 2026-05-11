package linear

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/oauth"
	"github.com/revelara-ai/orion/internal/trackers"
	"github.com/revelara-ai/orion/internal/trackers/conformance"
)

// stubLinear is a single httptest.Server that handles BOTH the
// GraphQL endpoint and the OAuth token endpoint. The tests construct
// an Adapter pointed at this server.
type stubLinear struct {
	mu sync.Mutex

	// state
	issues       map[string]map[string]any // identifier -> linear issue node
	createCount  int
	commentCount int
	updateCount  int
	healthCount  int
	refreshCount int

	// rotating tokens; tests check the persist callback gets fired
	currentAccess  string
	currentRefresh string

	// toggles
	failRefresh bool
	failHealth  bool
}

func newStub() *stubLinear {
	s := &stubLinear{
		issues:         map[string]map[string]any{},
		currentAccess:  "init-access",
		currentRefresh: "init-refresh",
	}
	// Seed three issues spanning a midpoint timestamp so the conformance
	// IncrementalSince subtest has rows on both sides of the boundary.
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	midpoint := now.Add(-30 * time.Minute)
	s.issues["STUB-1"] = linearIssueNode("li-1", "STUB-1", "Old issue", "before midpoint",
		"unstarted", now.Add(-2*time.Hour), []string{"bug"})
	s.issues["STUB-2"] = linearIssueNode("li-2", "STUB-2", "Mid issue", "at midpoint",
		"unstarted", midpoint, []string{"reliability"})
	s.issues["STUB-3"] = linearIssueNode("li-3", "STUB-3", "Fresh issue", "after midpoint",
		"started", now, []string{"reliability", "p1"})
	return s
}

func linearIssueNode(id, identifier, title, desc, stateType string, updatedAt time.Time, labels []string) map[string]any {
	labelNodes := make([]any, 0, len(labels))
	for _, l := range labels {
		labelNodes = append(labelNodes, map[string]any{"name": l})
	}
	return map[string]any{
		"id":          id,
		"identifier":  identifier,
		"title":       title,
		"description": desc,
		"url":         fmt.Sprintf("https://linear.app/test/issue/%s", identifier),
		"updatedAt":   updatedAt.Format(time.RFC3339),
		"state": map[string]any{
			"name": stateType,
			"type": stateType,
		},
		"labels": map[string]any{
			"nodes": labelNodes,
		},
	}
}

func (s *stubLinear) server(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.refreshCount++
		if s.failRefresh {
			http.Error(w, "refresh denied", http.StatusUnauthorized)
			return
		}
		_ = r.ParseForm()
		s.currentAccess = "rotated-access-" + r.FormValue("grant_type")
		s.currentRefresh = "rotated-refresh-" + r.FormValue("grant_type")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  s.currentAccess,
			"refresh_token": s.currentRefresh,
			"expires_in":    3600,
			"scope":         "read,write",
			"token_type":    "Bearer",
		})
	})
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		s.mu.Lock()
		defer s.mu.Unlock()

		writeData := func(data map[string]any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		}

		q := req.Query
		// Match on the GraphQL operation NAME (the second word after
		// "query"/"mutation") rather than substrings of the body, since
		// response selection sets share field names across queries.
		op := operationName(q)
		switch op {
		case "HealthCheck":
			s.healthCount++
			if s.failHealth {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"errors":[{"message":"auth"}]}`))
				return
			}
			writeData(map[string]any{"viewer": map[string]any{"id": "v1"}})
		case "FetchCandidates":
			// Filter by updatedAt gte if present
			var since time.Time
			if f, ok := req.Variables["filter"].(map[string]any); ok {
				if u, ok := f["updatedAt"].(map[string]any); ok {
					if g, ok := u["gte"].(string); ok {
						since, _ = time.Parse(time.RFC3339, g)
					}
				}
			}
			nodes := []any{}
			for _, ident := range orderedKeys(s.issues) {
				node := s.issues[ident]
				ut, _ := time.Parse(time.RFC3339, node["updatedAt"].(string))
				if !since.IsZero() && ut.Before(since) {
					continue
				}
				nodes = append(nodes, node)
			}
			writeData(map[string]any{
				"issues": map[string]any{
					"nodes":    nodes,
					"pageInfo": map[string]any{"hasNextPage": false, "endCursor": ""},
				},
			})
		case "FetchByIdentifiers":
			// FetchByExternalIDs: identifier in
			ids := []string{}
			if f, ok := req.Variables["filter"].(map[string]any); ok {
				if id, ok := f["identifier"].(map[string]any); ok {
					if list, ok := id["in"].([]any); ok {
						for _, v := range list {
							if sv, ok := v.(string); ok {
								ids = append(ids, sv)
							}
						}
					}
				}
			}
			nodes := []any{}
			for _, ident := range ids {
				if node, ok := s.issues[ident]; ok {
					nodes = append(nodes, node)
				}
			}
			writeData(map[string]any{
				"issues": map[string]any{
					"nodes":    nodes,
					"pageInfo": map[string]any{"hasNextPage": false, "endCursor": ""},
				},
			})
		case "IssueCreate":
			s.createCount++
			title, _ := req.Variables["title"].(string)
			desc, _ := req.Variables["description"].(string)
			id := fmt.Sprintf("li-new-%d", s.createCount)
			identifier := fmt.Sprintf("STUB-NEW-%d", s.createCount)
			node := linearIssueNode(id, identifier, title, desc, "unstarted", time.Now().UTC(), nil)
			s.issues[identifier] = node
			writeData(map[string]any{
				"issueCreate": map[string]any{
					"success": true,
					"issue":   node,
				},
			})
		case "IssueUpdate":
			s.updateCount++
			writeData(map[string]any{
				"issueUpdate": map[string]any{"success": true},
			})
		case "CommentCreate":
			s.commentCount++
			writeData(map[string]any{
				"commentCreate": map[string]any{"success": true},
			})
		default:
			http.Error(w, "unhandled query: "+q, http.StatusBadRequest)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// operationName returns the GraphQL operation name (the word after
// "query"/"mutation" up to the next paren or whitespace). Returns ""
// when no operation name is present (anonymous query).
func operationName(q string) string {
	trimmed := strings.TrimSpace(q)
	for _, kw := range []string{"query", "mutation", "subscription"} {
		if strings.HasPrefix(trimmed, kw) {
			rest := strings.TrimSpace(trimmed[len(kw):])
			// operation name terminates at '(', '{', or whitespace.
			end := len(rest)
			for i, r := range rest {
				if r == '(' || r == '{' || r == ' ' || r == '\t' || r == '\n' {
					end = i
					break
				}
			}
			return rest[:end]
		}
	}
	return ""
}

// orderedKeys returns the map's keys in sorted order so test output is stable.
func orderedKeys(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// simple sort
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// makeAdapter builds an Adapter pointed at the stub server, with the
// stub's tokens preloaded as if they came from a binding.
func makeAdapter(t *testing.T, stub *stubLinear) (*Adapter, trackers.TrackerBinding) {
	t.Helper()
	srv := stub.server(t)
	// Endpoints carved out of srv.URL: GraphQL at /graphql, token at /oauth/token.
	binding := trackers.TrackerBinding{
		Kind: trackers.TrackerKindLinear,
		Config: map[string]any{
			"workspace_slug": "stub",
			"team_id":        "TEAM_STUB",
			// Endpoint overrides; production reads default Linear URLs.
			"_api_url":   srv.URL + "/graphql",
			"_token_url": srv.URL + "/oauth/token",
		},
		Credentials: trackers.Credentials{
			OAuth2AccessToken:  stub.currentAccess,
			OAuth2RefreshToken: stub.currentRefresh,
			// ExpiresAt left zero so refreshIfNeeded skips the refresh
			// path unless a test explicitly sets it in the past.
			Extra: map[string]string{
				"client_id":     "test-client",
				"client_secret": "test-secret",
			},
		},
	}
	return NewAdapter(), binding
}

// makeAdapterExpiredToken seeds an already-expired token so the next
// call triggers refreshIfNeeded and exercises the rotation path.
func makeAdapterExpiredToken(t *testing.T, stub *stubLinear) (*Adapter, trackers.TrackerBinding) {
	a, b := makeAdapter(t, stub)
	b.Credentials.ExpiresAt = time.Now().Add(-1 * time.Hour)
	return a, b
}

// --- TESTS ---

func TestAdapter_HealthCheck(t *testing.T) {
	stub := newStub()
	a, b := makeAdapter(t, stub)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.HealthCheck(ctx, b); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if stub.healthCount != 1 {
		t.Errorf("healthCount = %d, want 1", stub.healthCount)
	}
}

func TestAdapter_HealthCheck_Unauthenticated(t *testing.T) {
	stub := newStub()
	stub.failHealth = true
	a, b := makeAdapter(t, stub)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := a.HealthCheck(ctx, b)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAdapter_FetchCandidates_AllIssues(t *testing.T) {
	stub := newStub()
	a, b := makeAdapter(t, stub)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := a.FetchCandidates(ctx, b, time.Time{})
	if err != nil {
		t.Fatalf("FetchCandidates: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d issues, want 3", len(got))
	}
	for _, issue := range got {
		if issue.ExternalID == "" {
			t.Errorf("empty ExternalID")
		}
		if !strings.HasPrefix(issue.ExternalID, "linear:") {
			t.Errorf("ExternalID %q missing linear: prefix", issue.ExternalID)
		}
	}
}

func TestAdapter_FetchCandidates_IncrementalSince(t *testing.T) {
	stub := newStub()
	a, b := makeAdapter(t, stub)
	since := time.Date(2026, 5, 11, 11, 35, 0, 0, time.UTC) // between STUB-1 and STUB-2 timestamps
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := a.FetchCandidates(ctx, b, since)
	if err != nil {
		t.Fatalf("FetchCandidates(since): %v", err)
	}
	// STUB-2 (midpoint=11:30, before since) and STUB-3 (12:00). Wait —
	// midpoint is 11:30 which is BEFORE since=11:35, so STUB-2 is
	// excluded; only STUB-3 should return.
	if len(got) != 1 {
		t.Errorf("got %d issues, want 1", len(got))
	}
	for _, issue := range got {
		if issue.LastUpdated.Before(since) {
			t.Errorf("issue %s: LastUpdated %v before since %v",
				issue.ExternalID, issue.LastUpdated, since)
		}
	}
}

func TestAdapter_FetchByExternalIDs(t *testing.T) {
	stub := newStub()
	a, b := makeAdapter(t, stub)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := a.FetchByExternalIDs(ctx, b, []string{"linear:stub#STUB-2"})
	if err != nil {
		t.Fatalf("FetchByExternalIDs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].ExternalID != "linear:stub#STUB-2" {
		t.Errorf("got %q, want linear:stub#STUB-2", got[0].ExternalID)
	}
}

func TestAdapter_Create(t *testing.T) {
	stub := newStub()
	a, b := makeAdapter(t, stub)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := a.Create(ctx, b, trackers.IssueDraft{
		Title:  "new issue",
		Body:   "body text",
		Labels: []string{"orion-filed"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ExternalID == "" {
		t.Error("empty ExternalID")
	}
	if stub.createCount != 1 {
		t.Errorf("createCount = %d, want 1", stub.createCount)
	}
}

func TestAdapter_UpdateState(t *testing.T) {
	stub := newStub()
	a, b := makeAdapter(t, stub)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.UpdateState(ctx, b, "linear:stub#STUB-1", trackers.StateClosed); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}
	if stub.updateCount != 1 {
		t.Errorf("updateCount = %d, want 1", stub.updateCount)
	}
}

func TestAdapter_Comment(t *testing.T) {
	stub := newStub()
	a, b := makeAdapter(t, stub)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Comment(ctx, b, "linear:stub#STUB-1", "hello"); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	if stub.commentCount != 1 {
		t.Errorf("commentCount = %d, want 1", stub.commentCount)
	}
}

func TestAdapter_Capabilities(t *testing.T) {
	a := NewAdapter()
	c := a.Capabilities()
	if !c.CanCreate || !c.CanUpdateState || !c.CanComment {
		t.Errorf("expected all write caps, got %+v", c)
	}
}

func TestAdapter_TokenRefreshFiresCallback(t *testing.T) {
	stub := newStub()
	a, b := makeAdapterExpiredToken(t, stub)

	var gotAccess, gotRefresh string
	a.persistFactory = func(_ trackers.TrackerBinding) oauth.PersistFunc {
		return func(_ context.Context, access, refresh string, _ time.Time) error {
			gotAccess = access
			gotRefresh = refresh
			return nil
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.HealthCheck(ctx, b); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if stub.refreshCount != 1 {
		t.Errorf("refreshCount = %d, want 1", stub.refreshCount)
	}
	if !strings.HasPrefix(gotAccess, "rotated-access-") {
		t.Errorf("persist callback did not fire with rotated access (got %q)", gotAccess)
	}
	if !strings.HasPrefix(gotRefresh, "rotated-refresh-") {
		t.Errorf("persist callback did not fire with rotated refresh (got %q)", gotRefresh)
	}
}

// TestAdapter_KindAndFactoryRegistration ensures the package's init()
// installed the Linear adapter under the linear kind.
func TestAdapter_KindAndFactoryRegistration(t *testing.T) {
	got, err := trackers.NewByKind(trackers.TrackerKindLinear)
	if err != nil {
		t.Fatalf("NewByKind(linear): %v", err)
	}
	if got.Kind() != trackers.TrackerKindLinear {
		t.Errorf("Kind() = %q, want %q", got.Kind(), trackers.TrackerKindLinear)
	}
}

// TestConformance runs the trackers/conformance suite against the
// Linear adapter pointed at the stub server.
func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.SuiteOptions{
		Factory: func(t *testing.T) (trackers.TrackerAdapter, trackers.TrackerBinding, conformance.Fixture) {
			stub := newStub()
			a, b := makeAdapter(t, stub)
			now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
			midpoint := now.Add(-30 * time.Minute)
			f := conformance.Fixture{
				MidpointTime: midpoint,
				Issues: []trackers.NormalizedIssue{
					{ExternalID: "linear:stub#STUB-1", LastUpdated: now.Add(-2 * time.Hour), State: trackers.StateOpen, Title: "Old"},
					{ExternalID: "linear:stub#STUB-2", LastUpdated: midpoint, State: trackers.StateOpen, Title: "Mid"},
					{ExternalID: "linear:stub#STUB-3", LastUpdated: now, State: trackers.StateInProgress, Title: "Fresh"},
				},
			}
			return a, b, f
		},
	})
}

// Token-refresh interaction wiring smoke: confirm SetTokenRefreshCallback
// is callable on the client implementation. (The Adapter itself does NOT
// implement TokenRefresher because tokens live on the per-binding client;
// the persistFactory plumbing is the per-binding equivalent.)
func TestAdapter_NotARefresher(t *testing.T) {
	if _, ok := any(NewAdapter()).(interface {
		SetTokenRefreshCallback(fn func(string, string, time.Time))
	}); ok {
		t.Error("Adapter should not implement TokenRefresher at the package level; refresh is per-binding via persistFactory")
	}
}
