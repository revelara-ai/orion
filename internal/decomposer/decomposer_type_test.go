package decomposer

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

func taskByKey(t *testing.T, epic Epic, key string) Task {
	t.Helper()
	for _, tk := range epic.Tasks {
		if tk.Key == key {
			return tk
		}
	}
	t.Fatalf("no task with key %q in epic", key)
	return Task{}
}

// or-3ba.1 acceptance: a non-http projectType yields a task tree WITHOUT HTTP-specific
// obligations; the http-service functional obligation is unchanged; both trees still
// pass the generic coverage gate.
func TestDecomposePerTypeFunctionalTask(t *testing.T) {
	es := acceptedSpec(t)

	httpEpic := Decompose(es, "http-service")
	cliEpic := Decompose(es, "cli")

	httpFn := taskByKey(t, httpEpic, "handler")
	if !strings.Contains(httpFn.ProofObligation, "GET ") {
		t.Fatalf("http-service functional task should keep its HTTP obligation, got %q", httpFn.ProofObligation)
	}

	cliFn := taskByKey(t, cliEpic, "handler")
	for _, httpToken := range []string{"GET ", "listens on port", "ResponseContract"} {
		if strings.Contains(cliFn.ProofObligation, httpToken) {
			t.Fatalf("non-http functional obligation must not bake in HTTP (%q): %q", httpToken, cliFn.ProofObligation)
		}
	}
	if len(cliFn.Covers) == 0 || cliFn.Covers[0] != string(completeness.DimFunctional) {
		t.Fatalf("non-http functional task must still cover the functional dimension: %+v", cliFn)
	}

	for name, epic := range map[string]Epic{"http-service": httpEpic, "cli": cliEpic} {
		if err := CoverageGate(es, epic); err != nil {
			t.Fatalf("%s tree failed the coverage gate: %v", name, err)
		}
	}
}
