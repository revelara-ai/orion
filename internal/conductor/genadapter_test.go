package conductor

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/sandbox"
)

// TestGenAdapterRegistry (or-4y7.3): Go is the default generation adapter; an
// unregistered language resolves to nil; the Go preamble + write-hint dispatch
// is byte-identical to the V2.0 literals.
func TestGenAdapterRegistry(t *testing.T) {
	if genFor("") == nil || genFor("") != genFor("go") || genFor("go").Language() != "go" {
		t.Fatal(`genFor("") must resolve to the go adapter`)
	}
	if genFor("ruby") != nil {
		t.Fatal("an unregistered language must resolve to nil")
	}

	// The dispatched Go preamble equals the direct writeDefaultPreamble output.
	gs := sandbox.GenSpec{Module: "svc", Route: "/time", Port: 8080, Format: "json"}
	var viaAdapter, direct strings.Builder
	genFor(gs.Language).Preamble(&viaAdapter, gs, "svc")
	writeDefaultPreamble(&direct, gs, "svc")
	if viaAdapter.String() != direct.String() {
		t.Fatalf("go preamble must be byte-identical through the adapter:\n--adapter--\n%s\n--direct--\n%s", viaAdapter.String(), direct.String())
	}
	if genFor("go").WriteHint() != "Write go.mod and main.go via write_file, then end your turn." {
		t.Fatalf("go write-hint changed: %q", genFor("go").WriteHint())
	}
}
