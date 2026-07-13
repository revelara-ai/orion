package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFixtureServiceHonorsPortEnv (or-7y68): the emitted service defers to
// $PORT over the spec's fixed port — the contract the empirical harness relies
// on to run proofs on OS-assigned ephemeral ports (freePort → PORT env), which
// is what makes concurrent proofs collision-free. The spec port stays the
// no-env default. Pinned as a source contract so a template edit can't
// silently re-hardcode the port.
func TestFixtureServiceHonorsPortEnv(t *testing.T) {
	dir := t.TempDir()
	if _, err := GenerateTimeServiceFixture(dir, GenSpec{Module: "orion-generated/service", Route: "/time", Port: 8080, Format: "json"}); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	// The exact override idiom, pinned: any non-empty $PORT replaces the spec
	// port. (A neutered condition — e.g. comparing $PORT to a sentinel — keeps
	// the Getenv call but kills the override; matching the full idiom catches
	// that. The heavy lane's TestBuildAndProveFixture is the semantic proof —
	// the harness probes the fixture on an ephemeral $PORT.)
	override := `if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}`
	if !strings.Contains(code, override) {
		t.Fatalf("the emitted service lost the $PORT override idiom — ephemeral-port proofs would break:\n%s", code)
	}
	// The $PORT override must come AFTER the fixed default so it WINS, and the
	// spec port stays only as the no-env default.
	fixedIdx := strings.Index(code, `":8080"`)
	if fixedIdx == -1 {
		t.Fatal("the spec port must remain the no-env default")
	}
	if strings.Index(code, override) < fixedIdx {
		t.Fatal("the $PORT override must come AFTER the fixed default so it wins")
	}
}
