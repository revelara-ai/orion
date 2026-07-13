package decomposer

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// TestHTTPFunctionalTaskDomainNeutral (or-hn15.6 DONE-WHEN e): the http-service
// functional task keeps its HTTP shape (route/port/contract) but no longer
// hardcodes the time-service domain ("current time in <tz>") — an HTTP intent
// that is not a clock gets a coherent obligation.
func TestHTTPFunctionalTaskDomainNeutral(t *testing.T) {
	es := spec.ExecutableSpec{Decisions: map[string]string{"response_format": "json"}}
	es.ResponseContract.Route = "/recipes"
	es.ResponseContract.Port = 8080
	// No TimeZone set — a generic API.
	ep := Decompose(es, "http-service")
	var handler Task
	for _, tk := range ep.Tasks {
		if tk.Key == "handler" {
			handler = tk
		}
	}
	if handler.Key == "" {
		t.Fatal("expected an http handler task")
	}
	blob := handler.Title + " || " + handler.ProofObligation
	if strings.Contains(strings.ToLower(blob), "time in") || strings.Contains(strings.ToLower(blob), "current time") {
		t.Fatalf("the http task must not hardcode the time-service domain: %q", blob)
	}
	// It keeps the HTTP shape: route + port + contract.
	if !strings.Contains(handler.ProofObligation, "/recipes") || !strings.Contains(handler.ProofObligation, "8080") {
		t.Fatalf("the http task must keep its route/port obligation: %q", handler.ProofObligation)
	}
}
