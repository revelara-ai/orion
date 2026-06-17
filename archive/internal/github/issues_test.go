package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestListIssues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/issues" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("state") != "open" {
			t.Errorf("state = %s", r.URL.Query().Get("state"))
		}
		if r.URL.Query().Get("labels") != "bug,p1" {
			t.Errorf("labels = %s", r.URL.Query().Get("labels"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"number": 1, "title": "first", "state": "open", "html_url": "https://gh/o/r/1", "updated_at": "2026-05-11T10:00:00Z"},
			{"number": 2, "title": "second-PR", "state": "open", "html_url": "https://gh/o/r/2", "updated_at": "2026-05-11T11:00:00Z", "pull_request": map[string]any{}},
		})
	}))
	defer srv.Close()
	app := stubApp(t, srv.URL)
	got, err := app.ListIssues(context.Background(), "o", "r", ListIssuesOptions{
		State:  "open",
		Labels: []string{"bug", "p1"},
	})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2 (PR + issue)", len(got))
	}
	if got[0].Number != 1 || got[1].PullRequest == nil {
		t.Errorf("unexpected payload: %+v", got)
	}
}

func TestListIssuesValidation(t *testing.T) {
	app := stubApp(t, "http://invalid")
	if _, err := app.ListIssues(context.Background(), "", "r", ListIssuesOptions{}); err == nil {
		t.Error("expected error for empty owner")
	}
}

func TestGetIssue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/issues/42" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 42, "title": "the answer", "state": "open", "updated_at": "2026-05-11T12:00:00Z",
		})
	}))
	defer srv.Close()
	app := stubApp(t, srv.URL)
	got, err := app.GetIssue(context.Background(), "o", "r", 42)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Title != "the answer" {
		t.Errorf("title = %s", got.Title)
	}
}

func TestCreateIssue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/o/r/issues" {
			t.Errorf("req = %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["title"] != "hello" {
			t.Errorf("title = %v", body["title"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 99, "title": body["title"], "state": "open", "html_url": "https://gh/o/r/99", "updated_at": "2026-05-11T13:00:00Z",
		})
	}))
	defer srv.Close()
	app := stubApp(t, srv.URL)
	got, err := app.CreateIssue(context.Background(), "o", "r", CreateIssueOptions{
		Title: "hello",
		Body:  "body",
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if got.Number != 99 {
		t.Errorf("number = %d", got.Number)
	}
}

func TestUpdateIssueState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %s", r.Method)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["state"] != "closed" {
			t.Errorf("state = %s", body["state"])
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()
	app := stubApp(t, srv.URL)
	if err := app.UpdateIssueState(context.Background(), "o", "r", 7, "closed"); err != nil {
		t.Errorf("UpdateIssueState: %v", err)
	}
}

func TestUpdateIssueStateRejectsBadInputs(t *testing.T) {
	app := stubApp(t, "http://invalid")
	if err := app.UpdateIssueState(context.Background(), "o", "r", 7, "wat"); err == nil {
		t.Error("expected error for bad state")
	}
	if err := app.UpdateIssueState(context.Background(), "", "r", 7, "open"); err == nil {
		t.Error("expected error for empty owner")
	}
}

func TestHealthCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rate_limit" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resources":{"core":{"limit":5000,"remaining":4999,"reset":0}}}`))
	}))
	defer srv.Close()
	app := stubApp(t, srv.URL)
	rl, err := app.HealthCheck(context.Background())
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if rl.Resources.Core.Limit != 5000 {
		t.Errorf("limit = %d", rl.Resources.Core.Limit)
	}
}

func TestListIssuesIncludesSince(t *testing.T) {
	captured := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()
	app := stubApp(t, srv.URL)
	since := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if _, err := app.ListIssues(context.Background(), "o", "r", ListIssuesOptions{Since: since}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(captured, "since=") {
		t.Errorf("query missing since: %s", captured)
	}
	if !strings.Contains(captured, "2026-05-01T00") {
		t.Errorf("since not formatted: %s", captured)
	}
}
