package polaris

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := NewClient(Config{
		BaseURL: srv.URL,
		APIKey:  "test-key",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestListKnowledgeInsights(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/knowledge/insights" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("auth = %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"insights": []map[string]any{{"id": "i1", "title": "Use timeouts", "body": "always"}},
			"total":    1,
		})
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	got, err := c.ListKnowledgeInsights(context.Background(), KnowledgeInsightsOptions{Tags: []string{"go"}, Limit: 5})
	if err != nil {
		t.Fatalf("ListKnowledgeInsights: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Use timeouts" {
		t.Errorf("got %+v", got)
	}
}

func TestSearchRequiresQuery(t *testing.T) {
	c, _ := NewClient(Config{BaseURL: "http://x", APIKey: "k"})
	if _, err := c.Search(context.Background(), SearchOptions{}); err == nil {
		t.Error("expected error for empty query")
	}
}

func TestSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/search" || r.Method != http.MethodPost {
			t.Errorf("req = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hits":  []map[string]any{{"id": "h1", "title": "incident", "score": 0.9}},
			"total": 1,
		})
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	hits, err := c.Search(context.Background(), SearchOptions{Query: "timeout"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != "h1" {
		t.Errorf("got %+v", hits)
	}
}

func TestForesightRequiresAnchor(t *testing.T) {
	c, _ := NewClient(Config{BaseURL: "http://x", APIKey: "k"})
	if _, err := c.Foresight(context.Background(), ForesightOptions{}); err == nil {
		t.Error("expected error for empty anchor")
	}
}

func TestForesight(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/knowledge/foresight" || r.Method != http.MethodPost {
			t.Errorf("req = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"chains": []map[string]any{{"id": "c1", "steps": []string{"a", "b"}}},
		})
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	got, err := c.Foresight(context.Background(), ForesightOptions{Anchor: "add timeout"})
	if err != nil {
		t.Fatalf("Foresight: %v", err)
	}
	if len(got) != 1 || got[0].ID != "c1" {
		t.Errorf("got %+v", got)
	}
}

func TestListApplicableRisks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/risks" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("status"); got != "applicable" {
			t.Errorf("status query = %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"risks": []map[string]any{{"id": "R-1", "control_code": "RC-T-1", "service": "frontend", "status": "applicable", "summary": "missing timeout"}},
			"total": 1,
		})
	}))
	defer srv.Close()
	c := newTestClient(t, srv)
	got, err := c.ListApplicableRisks(context.Background(), ListApplicableRisksOptions{Service: "frontend"})
	if err != nil {
		t.Fatalf("ListApplicableRisks: %v", err)
	}
	if len(got) != 1 || got[0].ID != "R-1" {
		t.Errorf("got %+v", got)
	}
}
