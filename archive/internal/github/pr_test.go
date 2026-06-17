package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stubApp is an App pre-loaded with a cached install token, used by
// PR/comment tests so they don't need to mint JWTs.
func stubApp(t *testing.T, baseURL string) *App {
	t.Helper()
	app, err := NewApp(AppConfig{
		AppID: 1, InstallationID: 1,
		PrivateKeyPEM: generateTestKey(t),
		APIBaseURL:    baseURL,
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	app.cachedToken = "ghs_stubtoken"
	app.cachedExpiry = time.Now().Add(1 * time.Hour)
	return app
}

func TestCreatePR(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/revelara-ai/microservices-demo/pulls" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		if got["title"] != "Hello Orion" {
			t.Errorf("title = %v", got["title"])
		}
		if got["head"] != "orion/r3dq8a-hello" {
			t.Errorf("head = %v", got["head"])
		}
		if got["base"] != "main" {
			t.Errorf("base = %v", got["base"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number":   42,
			"html_url": "https://github.com/revelara-ai/microservices-demo/pull/42",
			"node_id":  "node_xyz",
		})
	}))
	defer srv.Close()

	app := stubApp(t, srv.URL)
	pr, err := app.CreatePR(context.Background(), PROptions{
		Owner: "revelara-ai", Repo: "microservices-demo",
		Head: "orion/r3dq8a-hello", Base: "main",
		Title: "Hello Orion", Body: "test",
	})
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if pr.Number != 42 {
		t.Errorf("number = %d, want 42", pr.Number)
	}
	if !strings.HasSuffix(pr.HTMLURL, "/pull/42") {
		t.Errorf("html_url = %s", pr.HTMLURL)
	}
}

func TestCreatePRValidation(t *testing.T) {
	app := stubApp(t, "http://invalid")
	cases := []PROptions{
		{Repo: "r", Head: "h", Base: "b", Title: "t"},  // no owner
		{Owner: "o", Head: "h", Base: "b", Title: "t"}, // no repo
		{Owner: "o", Repo: "r", Base: "b", Title: "t"}, // no head
		{Owner: "o", Repo: "r", Head: "h", Title: "t"}, // no base
		{Owner: "o", Repo: "r", Head: "h", Base: "b"},  // no title
	}
	for i, c := range cases {
		if _, err := app.CreatePR(context.Background(), c); err == nil {
			t.Errorf("case %d: expected validation error, got nil", i)
		}
	}
}

func TestPostComment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/o/r/issues/7/comments" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		if got["body"] != "test comment" {
			t.Errorf("body = %v", got["body"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"html_url": "https://github.com/o/r/pull/7#issuecomment-1",
		})
	}))
	defer srv.Close()
	app := stubApp(t, srv.URL)
	url, err := app.PostComment(context.Background(), "o", "r", 7, "test comment")
	if err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	if !strings.Contains(url, "issuecomment-1") {
		t.Errorf("comment url = %s", url)
	}
}

func TestPostCommentValidation(t *testing.T) {
	app := stubApp(t, "http://invalid")
	if _, err := app.PostComment(context.Background(), "", "r", 1, "x"); err == nil {
		t.Error("expected error for empty owner")
	}
	if _, err := app.PostComment(context.Background(), "o", "r", 0, "x"); err == nil {
		t.Error("expected error for zero PR number")
	}
	if _, err := app.PostComment(context.Background(), "o", "r", 1, ""); err == nil {
		t.Error("expected error for empty body")
	}
}
