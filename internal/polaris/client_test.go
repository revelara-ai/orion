package polaris_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/polaris"
)

func TestConfig_LoadFromEnv(t *testing.T) {
	t.Setenv("POLARIS_BASE_URL", "https://polaris.example.com")
	t.Setenv("POLARIS_API_KEY", "secret-key")
	cfg, err := polaris.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://polaris.example.com" {
		t.Errorf("BaseURL=%q", cfg.BaseURL)
	}
	if cfg.APIKey != "secret-key" {
		t.Errorf("APIKey=%q", cfg.APIKey)
	}
}

func TestConfig_LoadFromEnv_MissingBaseURL(t *testing.T) {
	t.Setenv("POLARIS_BASE_URL", "")
	t.Setenv("POLARIS_API_KEY", "k")
	_, err := polaris.LoadFromEnv()
	if !errors.Is(err, polaris.ErrInvalidConfig) {
		t.Errorf("err=%v; want ErrInvalidConfig", err)
	}
}

func TestConfig_LoadFromEnv_MissingAPIKey(t *testing.T) {
	t.Setenv("POLARIS_BASE_URL", "https://x")
	t.Setenv("POLARIS_API_KEY", "")
	_, err := polaris.LoadFromEnv()
	if !errors.Is(err, polaris.ErrAuthMissing) {
		t.Errorf("err=%v; want ErrAuthMissing", err)
	}
}

func TestClient_ListControls_SendsBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = fmt.Fprint(w, `{"controls":[],"total":0,"page":1,"limit":100}`)
	}))
	defer srv.Close()

	c, err := polaris.NewClient(polaris.Config{BaseURL: srv.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ListControls(context.Background(), polaris.ListControlsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization header=%q; want 'Bearer test-key'", gotAuth)
	}
}

func TestClient_ListControls_PassesQueryParams(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		_, _ = fmt.Fprint(w, `{"controls":[],"total":0,"page":1,"limit":100}`)
	}))
	defer srv.Close()

	c, _ := polaris.NewClient(polaris.Config{BaseURL: srv.URL, APIKey: "k"})
	_, err := c.ListControls(context.Background(), polaris.ListControlsOptions{
		Categories: []string{"fault_tolerance", "change_management"},
		Limit:      50,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "/api/v1/controls?categories=fault_tolerance%2Cchange_management&limit=50"
	if gotURL != want {
		t.Errorf("URL=%q\n want=%q", gotURL, want)
	}
}

func TestClient_ListControls_ParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{
			"controls": [
				{"id":"00000000-0000-0000-0000-000000000001","control_code":"RC-001","name":"Timeouts","category":"fault_tolerance","type":"preventive","weight":3},
				{"id":"00000000-0000-0000-0000-000000000002","control_code":"RC-002","name":"Retry","category":"fault_tolerance","type":"preventive","weight":2}
			],
			"total":2,"page":1,"limit":100
		}`)
	}))
	defer srv.Close()

	c, _ := polaris.NewClient(polaris.Config{BaseURL: srv.URL, APIKey: "k"})
	cat, err := c.ListControls(context.Background(), polaris.ListControlsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cat.Total != 2 {
		t.Errorf("Total=%d", cat.Total)
	}
	if len(cat.Controls) != 2 {
		t.Fatalf("got %d controls; want 2", len(cat.Controls))
	}
	if cat.Controls[0].ControlCode != "RC-001" {
		t.Errorf("Controls[0].ControlCode=%q", cat.Controls[0].ControlCode)
	}
	// ByCode lookup helper
	rc1 := cat.ByCode("RC-001")
	if rc1 == nil {
		t.Fatal("ByCode RC-001 returned nil")
	}
	if rc1.Category != "fault_tolerance" {
		t.Errorf("Category=%q", rc1.Category)
	}
	// ByCategory lookup helper
	ft := cat.ByCategory("fault_tolerance")
	if len(ft) != 2 {
		t.Errorf("ByCategory returned %d; want 2", len(ft))
	}
}

func TestClient_ListControls_RetriesOn503(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, `{"controls":[],"total":0,"page":1,"limit":100}`)
	}))
	defer srv.Close()

	c, _ := polaris.NewClient(polaris.Config{
		BaseURL:    srv.URL,
		APIKey:     "k",
		MaxRetries: 3,
		BaseDelay:  10 * time.Millisecond,
	})
	_, err := c.ListControls(context.Background(), polaris.ListControlsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls=%d; want 3 (2 failures + 1 success)", got)
	}
}

func TestClient_ListControls_FailsOn4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c, _ := polaris.NewClient(polaris.Config{BaseURL: srv.URL, APIKey: "k"})
	_, err := c.ListControls(context.Background(), polaris.ListControlsOptions{})
	if err == nil {
		t.Fatal("want error on 401, got nil")
	}
	if !errors.Is(err, polaris.ErrUnexpectedStatus) {
		t.Errorf("err=%v; want ErrUnexpectedStatus", err)
	}
}

func TestClient_ListControls_FailsOnMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `not json {`)
	}))
	defer srv.Close()

	c, _ := polaris.NewClient(polaris.Config{BaseURL: srv.URL, APIKey: "k"})
	_, err := c.ListControls(context.Background(), polaris.ListControlsOptions{})
	if err == nil {
		t.Fatal("want error on malformed JSON, got nil")
	}
}
