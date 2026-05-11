package oauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExecRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Errorf("auth = %q", auth)
		}
		body, _ := io.ReadAll(r.Body)
		var req graphqlRequest
		_ = json.Unmarshal(body, &req)
		if !strings.Contains(req.Query, "viewer") {
			t.Errorf("query missing 'viewer': %s", req.Query)
		}
		if req.Variables["id"] != "user-1" {
			t.Errorf("variables.id = %v", req.Variables["id"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"user-1","name":"Alice"}}}`))
	}))
	defer srv.Close()

	data, err := Exec(context.Background(), GraphQLExecOptions{
		Endpoint:    srv.URL,
		BearerToken: "test-token",
	}, "query($id: String!) { viewer(id: $id) { id name } }", map[string]any{"id": "user-1"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	viewer, ok := data["viewer"].(map[string]any)
	if !ok {
		t.Fatalf("data.viewer not map: %T", data["viewer"])
	}
	if viewer["id"] != "user-1" {
		t.Errorf("id = %v", viewer["id"])
	}
}

func TestExecSurfacesGraphQLErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"invalid token"}]}`))
	}))
	defer srv.Close()
	_, err := Exec(context.Background(), GraphQLExecOptions{
		Endpoint: srv.URL, BearerToken: "t",
	}, "query { viewer { id } }", nil)
	if err == nil || !strings.Contains(err.Error(), "invalid token") {
		t.Errorf("err = %v, want contains 'invalid token'", err)
	}
}

func TestExecRejectsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad creds"}`))
	}))
	defer srv.Close()
	_, err := Exec(context.Background(), GraphQLExecOptions{
		Endpoint: srv.URL, BearerToken: "t",
	}, "{}", nil)
	if err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Errorf("err = %v, want status 401", err)
	}
}

func TestExecValidatesInputs(t *testing.T) {
	if _, err := Exec(context.Background(), GraphQLExecOptions{}, "{}", nil); err == nil {
		t.Error("expected error for empty endpoint")
	}
	if _, err := Exec(context.Background(), GraphQLExecOptions{Endpoint: "http://x"}, "{}", nil); err == nil {
		t.Error("expected error for empty bearer token")
	}
}
