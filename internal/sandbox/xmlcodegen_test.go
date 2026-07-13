package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// or-hbc: the fixture generator emits a REAL application/xml artifact for an
// xml contract — never the JSON default.
func TestXMLFixtureCodegen(t *testing.T) {
	dir := t.TempDir()
	if _, err := GenerateTimeServiceFixture(dir, GenSpec{Module: "orion-generated/service", Route: "/time", Port: 8080, Format: "xml", TimeZone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if !strings.Contains(src, `application/xml`) || !strings.Contains(src, "<time>") {
		t.Fatalf("xml fixture missing the xml branch:\n%s", src)
	}
	if strings.Contains(src, `json.NewEncoder`) {
		t.Fatalf("xml fixture fell through to the JSON branch:\n%s", src)
	}
}
