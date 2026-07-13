package conductor

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// TestDirectionBuildRefusal (or-hn15.5 DONE-WHEN b): a ratified direction the
// harness cannot generate/prove (non-Go language, a real engine, a non-HTTP
// wire protocol) refuses the build NAMING the direction — never silently emits
// a Go artifact. A Go/HTTP direction (or none) builds as before.
func TestDirectionBuildRefusal(t *testing.T) {
	// Non-Go language: refused, named.
	cpp := spec.ExecutableSpec{Decisions: map[string]string{"direction.language": "cpp"}}
	err := directionBuildRefusal(cpp)
	if err == nil || !strings.Contains(err.Error(), "cpp") || !strings.Contains(err.Error(), "or-4rxw") {
		t.Fatalf("a ratified non-Go language must refuse the build naming the direction + tracker, got: %v", err)
	}

	// A real engine: refused.
	if err := directionBuildRefusal(spec.ExecutableSpec{Decisions: map[string]string{"direction.engine": "unreal"}}); err == nil {
		t.Fatal("a ratified game engine must refuse the Go build")
	}

	// A non-HTTP wire protocol: refused.
	if err := directionBuildRefusal(spec.ExecutableSpec{Decisions: map[string]string{"direction.wire_protocol": "grpc"}}); err == nil {
		t.Fatal("a ratified non-HTTP wire protocol must refuse the Go build")
	}

	// Go / http-json / none: buildable (no refusal) — the V2 path.
	for _, d := range []map[string]string{
		{"direction.language": "go", "direction.wire_protocol": "http-json", "direction.engine": "none"},
		{}, // no direction decisions at all (standard http-service)
	} {
		if err := directionBuildRefusal(spec.ExecutableSpec{Decisions: d}); err != nil {
			t.Fatalf("a Go/HTTP direction must build without refusal, got: %v", err)
		}
	}
}

// TestProvenanceNoteGatesRoute (or-hn15.5 DONE-WHEN f): the provenance note only
// prints the HTTP Route/Port/Format line when there IS a route — a non-HTTP
// artifact doesn't claim a phantom "/  port 0" endpoint.
func TestProvenanceNoteGatesRoute(t *testing.T) {
	http := spec.ExecutableSpec{Intent: "time service"}
	http.ResponseContract.Route = "/time"
	http.ResponseContract.Port = 8080
	if !strings.Contains(provenanceNote(http), "Route:") {
		t.Fatal("an HTTP artifact must keep its Route/Port provenance line")
	}

	game := spec.ExecutableSpec{Intent: "a PvE mech game"}
	if strings.Contains(provenanceNote(game), "Route:") {
		t.Fatalf("a non-HTTP artifact must not print a phantom Route/Port line:\n%s", provenanceNote(game))
	}
}
