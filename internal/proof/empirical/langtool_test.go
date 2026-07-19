package empirical

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/proof/testsynth"
)

// TestLangToolRegistry (or-4y7.6): Go is the default empirical tool; an
// unregistered language resolves to nil (never a silent Go build).
func TestLangToolRegistry(t *testing.T) {
	if langToolFor("") == nil || langToolFor("") != langToolFor("go") || langToolFor("go").Language() != "go" {
		t.Fatal(`langToolFor("") must resolve to the go tool`)
	}
	if langToolFor("ruby") != nil {
		t.Fatal("an unregistered language must resolve to nil")
	}
}

// TestProveRefusesUnregisteredLanguageEmpirical (or-4y7.6): a contract whose
// language has no registered tool is refused loudly — never go-built silently.
func TestProveRefusesUnregisteredLanguageEmpirical(t *testing.T) {
	_, _, err := Prove(context.Background(), t.TempDir(), testsynth.Contract{Language: "ruby", Route: "/x"})
	if err == nil || !strings.Contains(err.Error(), "ruby") {
		t.Fatalf("an unregistered language must refuse naming it, got %v", err)
	}
}
