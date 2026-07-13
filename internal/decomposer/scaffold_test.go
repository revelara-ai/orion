package decomposer

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

func scaffoldOf(e Epic) Task {
	for _, tk := range e.Tasks {
		if tk.Key == "scaffold" {
			return tk
		}
	}
	return Task{}
}

// TestScaffoldNeutralForUnregisteredNoLanguage (or-hn15.4 DONE-WHEN d): an
// unregistered project type with NO chosen language gets a language-neutral
// skeleton, not the Go scaffold — "unasked" is not "Go".
func TestScaffoldNeutralForUnregisteredNoLanguage(t *testing.T) {
	game := spec.ExecutableSpec{Intent: "a PvE mech game", Decisions: map[string]string{}}
	sc := scaffoldOf(Decompose(game, "game"))
	if strings.Contains(sc.Title, "Go module") || strings.Contains(sc.FileScope, "go.mod") {
		t.Fatalf("an unregistered type with no language must NOT get the Go scaffold, got %q / %q", sc.Title, sc.FileScope)
	}
	if sc.FileScope != "." {
		t.Fatalf("the neutral skeleton should scope the whole tree, got %q", sc.FileScope)
	}

	// Negative: a registered type (http-service) with no language keeps Go.
	http := spec.ExecutableSpec{Decisions: map[string]string{}}
	scHTTP := scaffoldOf(Decompose(http, "http-service"))
	if !strings.Contains(scHTTP.FileScope, "go.mod") {
		t.Fatalf("http-service must keep the Go scaffold, got %q", scHTTP.FileScope)
	}

	// Negative: an explicit go on an unregistered type honors Go.
	goGame := spec.ExecutableSpec{Decisions: map[string]string{"direction.language": "go"}}
	if !strings.Contains(scaffoldOf(Decompose(goGame, "game")).FileScope, "go.mod") {
		t.Fatal("an explicit direction.language=go must get the Go scaffold")
	}

	// Negative: an explicit non-go language gets the reduced-proof scaffold.
	cpp := spec.ExecutableSpec{Decisions: map[string]string{"direction.language": "cpp"}}
	scCpp := scaffoldOf(Decompose(cpp, "game"))
	if strings.Contains(scCpp.FileScope, "go.mod") || !strings.Contains(strings.ToLower(scCpp.Title), "cpp") {
		t.Fatalf("an explicit non-go language must get its own scaffold, got %q", scCpp.Title)
	}
}
