package api

import (
	"fmt"
	"net/http"
	"strings"
)

// Route describes a single API endpoint handler for the Orion REST surface (§21.4).
type Route struct {
	Method       string                                    // GET, POST, PUT, DELETE
	Path         string                                    // e.g., "/api/v1/runs/{id}"
	Handler      func(http.ResponseWriter, *http.Request) // standard HTTP handler function
	Middleware   []func(next http.Handler) http.Handler   // middleware chain
	Docs         string                                    // description for OpenAPI documentation
	AuthRequired bool                                      // whether this route requires authentication
}

// Validate checks that the Route has sane configuration values.
func (r *Route) Validate() error {
	if r.Method == "" {
		return fmt.Errorf("api.Validate: Method cannot be empty")
	}
	if !isValidMethod(r.Method) {
		return fmt.Errorf("api.Validate: unsupported HTTP method %q", r.Method)
	}
	if r.Path == "" {
		return fmt.Errorf("api.Validate: Path cannot be empty")
	}
	if !strings.HasPrefix(r.Path, "/") {
		return fmt.Errorf("api.Validate: path must start with /")
	}
	if r.Handler == nil {
		return fmt.Errorf("api.Validate: Handler cannot be nil")
	}
	return nil
}

// isValidMethod checks if the HTTP method is one of the allowed methods.
func isValidMethod(method string) bool {
	switch method {
	case "GET", "POST", "PUT", "DELETE":
		return true
	default:
		return false
	}
}

// BuildMiddleware applies middleware in order, returning a wrapped http.Handler.
func (r *Route) BuildMiddleware(base http.Handler) http.Handler {
	result := base
	for i := len(r.Middleware) - 1; i >= 0; i-- {
		result = r.Middleware[i](result)
	}
	return result
}

// RouteConfig allows fluent construction of routes.
type RouteConfig struct {
	method     string
	path       string
	handler    func(http.ResponseWriter, *http.Request)
	middleware []func(next http.Handler) http.Handler
	docs       string
	auth       bool
}

// NewRouteConfig creates a new empty RouteConfig for building routes.
func NewRouteConfig() *RouteConfig {
	return &RouteConfig{}
}

// Method sets the HTTP method and returns the config for chaining.
func (rc *RouteConfig) Method(m string) *RouteConfig {
	rc.method = m
	return rc
}

// Path sets the URL path and returns the config for chaining.
func (rc *RouteConfig) Path(p string) *RouteConfig {
	rc.path = p
	return rc
}

// HandlerFunc sets the handler function and returns the config for chaining.
func (rc *RouteConfig) HandlerFunc(h func(http.ResponseWriter, *http.Request)) *RouteConfig {
	rc.handler = h
	return rc
}

// Middleware adds a middleware function to the chain and returns the config.
func (rc *RouteConfig) Middleware(mw func(next http.Handler) http.Handler) *RouteConfig {
	rc.middleware = append(rc.middleware, mw)
	return rc
}

// WithDocs sets the OpenAPI documentation string and returns the config.
func (rc *RouteConfig) WithDocs(d string) *RouteConfig {
	rc.docs = d
	return rc
}

// RequireAuth marks this route as requiring authentication and returns the config.
func (rc *RouteConfig) RequireAuth() *RouteConfig {
	rc.auth = true
	return rc
}

// Build assembles a Route from the config, verifying sanity.
func (rc *RouteConfig) Build() (*Route, error) {
	route := &Route{
		Method:       rc.method,
		Path:         rc.path,
		Handler:      rc.handler,
		Middleware:   rc.middleware,
		Docs:         rc.docs,
		AuthRequired: rc.auth,
	}
	if err := route.Validate(); err != nil {
		return nil, err
	}
	return route, nil
}
