package api

import (
	"net/http"
	"testing"
)

// noopHandler is a simple no-op HTTP handler for tests.
func noopHandler(w http.ResponseWriter, r *http.Request) {}

func TestRouteValidate(t *testing.T) {
	tests := []struct {
		name    string
		route   Route
		wantErr bool
		errSub  string
	}{
		{
			name:    "Valid GET route",
			route:   Route{Method: "GET", Path: "/runs/{id}", Handler: noopHandler},
			wantErr: false,
		},
		{
			name:    "Valid POST route",
			route:   Route{Method: "POST", Path: "/runs", Handler: noopHandler, Docs: "CreateArun"},
			wantErr: false,
		},
		{
			name:    "Empty method fails",
			route:   Route{Path: "/runs"},
			wantErr: true,
			errSub:  "Method cannot be empty",
		},
		{
			name:    "Empty path fails",
			route:   Route{Method: "GET"},
			wantErr: true,
			errSub:  "Path cannot be empty",
		},
		{
			name:    "Unsupported method PATCH",
			route:   Route{Method: "PATCH", Path: "/test", Handler: noopHandler},
			wantErr: true,
			errSub:  "unsupported HTTP method",
		},
		{
			name:    "Path without slash",
			route:   Route{Method: "GET", Path: "runs", Handler: noopHandler},
			wantErr: true,
			errSub:  "must start with /",
		},
		{
			name:    "Nil handler fails",
			route:   Route{Method: "GET", Path: "/test"},
			wantErr: true,
			errSub:  "Handler cannot be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.route.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate error = %v, wantError %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil && !containsSubstring(err.Error(), tt.errSub) {
				t.Errorf("error %q does not contain expected %q", err.Error(), tt.errSub)
			}
		})
	}
}

func TestIsValidMethod(t *testing.T) {
	tests := []struct {
		method string
		want   bool
	}{
		{"GET", true},
		{"POST", true},
		{"PUT", true},
		{"DELETE", true},
		{"PATCH", false},
		{"OPTIONS", false},
		{"HEAD", false},
		{"CONNECT", false},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			got := isValidMethod(tt.method)
			if got != tt.want {
				t.Errorf("isValidMethod(%q) = %v, want %v", tt.method, got, tt.want)
			}
		})
	}
}

func TestBuildMiddleware(t *testing.T) {
	count := 0
	mw1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count++
			next.ServeHTTP(w, r)
		})
	}
	mw2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count++
			next.ServeHTTP(w, r)
		})
	}

	route := Route{
		Method: "GET", Path: "/test", Handler: noopHandler,
		Middleware: []func(next http.Handler) http.Handler{mw1, mw2},
	}

	result := route.BuildMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if result == nil {
		t.Error("middleware chain should not return nil")
	}
	if count != 0 {
		t.Errorf("count = %d, want 0 (ServeHTTP not invoked yet)", count)
	}

	// Invoke to verify middleware is applied
	result.ServeHTTP(nil, nil)
	if count != 2 {
		t.Errorf("middleware count = %d after ServeHTTP, want 2", count)
	}
}

func TestMultipleMethods(t *testing.T) {
	methods := []string{"GET", "POST", "PUT", "DELETE"}
	for _, method := range methods {
		if !isValidMethod(method) {
			t.Errorf("isValidMethod(%q) should be true for valid method", method)
		}
	}
}

func TestEdgeCaseRoutes(t *testing.T) {
	tests := []struct {
		name  string
		route Route
	}{
		{"Trailing slash", Route{Method: "GET", Path: "/runs/", Handler: noopHandler}},
		{"Deep path", Route{Method: "POST", Path: "/api/v1/runs/123/issues", Handler: noopHandler}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.route.Validate(); err != nil {
				t.Errorf("path=%q method=%q error = %v", tt.route.Path, tt.route.Method, err)
			}
		})
	}
}

func TestEmptyMiddlewareChain(t *testing.T) {
	route := Route{Method: "GET", Path: "/test", Handler: noopHandler}
	result := route.BuildMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if result == nil {
		t.Error("empty middleware should not return nil")
	}
	if _, ok := result.(http.Handler); !ok {
		t.Errorf("BuildMiddleware must return an http.Handler, got %T", result)
	}
}

func TestRouteConfigBuilder(t *testing.T) {
	route, err := NewRouteConfig().
		Method("POST").
		Path("/runs").
		HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}).
		WithDocs("A test route").
		RequireAuth().
		Build()

	if err != nil {
		t.Fatalf("Build error = %v", err)
	}
	if route.Method != "POST" {
		t.Errorf("route.Method = %q, want POST", route.Method)
	}
	if !route.AuthRequired {
		t.Error("AuthRequired should be true")
	}
	if route.Docs != "A test route" {
		t.Errorf("Docs = %q, want 'A test route'", route.Docs)
	}
}

func TestRouteConfigBuilderInvalid(t *testing.T) {
	route, err := NewRouteConfig().Method("").Build()
	if err == nil {
		t.Error("expect non-nil error for invalid method")
	}
	if route != nil {
		t.Error("route should be nil on validation failure")
	}
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
