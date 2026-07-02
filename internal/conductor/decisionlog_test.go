package conductor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextengine"
	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

const decisionArtifact = `package main

import (
	"net/http"
)

// GreetingFormat is the wire shape other modules must reuse.
type GreetingFormat struct {
	Message string ` + "`json:\"message\"`" + `
}

// RenderGreeting is the exported rendering hook.
func RenderGreeting(name string) string { return "hi " + name }

func handleGreet(w http.ResponseWriter, r *http.Request) {}

func main() {
	http.HandleFunc("/greet", handleGreet)
	_ = http.ListenAndServe(":8080", nil)
}
`

func decisionFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dogWrite(t, filepath.Join(dir, "go.mod"), "module orion-generated/greeter\n\ngo 1.23\n")
	dogWrite(t, filepath.Join(dir, "main.go"), decisionArtifact)
	return dir
}

func memStore(t *testing.T) *memory.Store {
	t.Helper()
	m, err := memory.Open(t.TempDir())
	if err != nil {
		t.Skipf("memory store unavailable: %v", err)
	}
	return m
}

// TestRememberDecidedConstraints: a PROVEN module's structural decisions —
// module path, exported symbols, served routes — are extracted from the artifact
// itself (trust wall: never the agent's narrative) and persisted as ONE
// proof-trust decision item.
func TestRememberDecidedConstraints(t *testing.T) {
	mem := memStore(t)
	defer mem.Close()
	ctx := context.Background()
	dir := decisionFixture(t)

	accept := proof.Report{}
	accept.Outcome.Verdict = truthalign.Accept
	if err := rememberDecidedConstraints(ctx, mem, "task-greeter", dir, accept); err != nil {
		t.Fatal(err)
	}

	items, err := mem.Retrieve(ctx, "greeter decisions", memory.STM, memory.MTM, memory.LTM)
	if err != nil {
		t.Fatal(err)
	}
	var found *memory.Item
	for i := range items {
		if items[i].Kind == memory.KindDecision && strings.Contains(items[i].Content, "task-greeter") {
			found = &items[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no KindDecision item persisted; got %d items", len(items))
	}
	if found.TrustTier != memory.TrustProof {
		t.Errorf("decisions derive from the proven artifact: want proof trust, got %q", found.TrustTier)
	}
	for _, want := range []string{"orion-generated/greeter", "RenderGreeting", "GreetingFormat", "/greet"} {
		if !strings.Contains(found.Content, want) {
			t.Errorf("decision log must carry %q, got:\n%s", want, found.Content)
		}
	}
	if strings.Contains(found.Content, "handleGreet") {
		t.Errorf("unexported symbols are not cross-module constraints:\n%s", found.Content)
	}
}

// TestRememberDecidedConstraintsSkipsNonAccept: an unproven module decides nothing.
func TestRememberDecidedConstraintsSkipsNonAccept(t *testing.T) {
	mem := memStore(t)
	defer mem.Close()
	ctx := context.Background()

	reject := proof.Report{}
	reject.Outcome.Verdict = truthalign.Reject
	if err := rememberDecidedConstraints(ctx, mem, "task-x", decisionFixture(t), reject); err != nil {
		t.Fatal(err)
	}
	items, _ := mem.Retrieve(ctx, "decisions", memory.STM, memory.MTM, memory.LTM)
	for _, it := range items {
		if it.Kind == memory.KindDecision {
			t.Fatalf("a rejected module must not log decisions: %+v", it)
		}
	}
}

// TestDecisionsReachDependentModuleContext: the whole point — module N's decided
// constraints appear in module N+1's assembled generation context.
func TestDecisionsReachDependentModuleContext(t *testing.T) {
	mem := memStore(t)
	defer mem.Close()
	ctx := context.Background()

	accept := proof.Report{}
	accept.Outcome.Verdict = truthalign.Accept
	if err := rememberDecidedConstraints(ctx, mem, "task-greeter", decisionFixture(t), accept); err != nil {
		t.Fatal(err)
	}

	eng := contextengine.New(nil, mem)
	bundle, err := eng.Assemble(ctx, "", "greeting module exports routes")
	if err != nil {
		t.Fatal(err)
	}
	rendered := bundle.Render(contextengine.DomainGeneration)
	if !strings.Contains(rendered, "RenderGreeting") || !strings.Contains(rendered, "/greet") {
		t.Fatalf("module N's decided constraints must reach module N+1's context, got:\n%s", rendered)
	}
}
