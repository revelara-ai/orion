package decomposer

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// TestScaffoldGatedOnDirectionLanguage (or-045a.5 DONE-WHEN d): a ratified
// non-Go direction stops the blind "Scaffold Go module" plan — the scaffold
// carries the direction; the default/Go path is byte-identical to V2.
func TestScaffoldGatedOnDirectionLanguage(t *testing.T) {
	cpp := Decompose(spec.ExecutableSpec{Decisions: map[string]string{"direction.language": "cpp"}}, "game")
	var scaffold Task
	for _, tk := range cpp.Tasks {
		if tk.Key == "scaffold" {
			scaffold = tk
		}
	}
	if strings.Contains(scaffold.Title, "Go module") {
		t.Fatalf("a ratified cpp direction must not emit the Go scaffold: %q", scaffold.Title)
	}
	if !strings.Contains(scaffold.Title, "cpp") || !strings.Contains(scaffold.ProofObligation, "reduced proof") {
		t.Fatalf("the scaffold must carry the direction + honest reduced-proof note: %+v", scaffold)
	}

	// Negative: default and explicit-go stay the V2 Go scaffold.
	for _, dec := range []map[string]string{nil, {"direction.language": "go"}} {
		e := Decompose(spec.ExecutableSpec{Decisions: dec}, "http-service")
		for _, tk := range e.Tasks {
			if tk.Key == "scaffold" && tk.Title != "Scaffold Go module and entrypoint" {
				t.Fatalf("the Go path must stay byte-identical, got %q", tk.Title)
			}
		}
	}
}
