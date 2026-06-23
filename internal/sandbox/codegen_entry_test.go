package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenSpecEntryDefault(t *testing.T) {
	if got := (GenSpec{}).Entry(); got != "handleTime" {
		t.Fatalf("default Entry() = %q, want handleTime", got)
	}
	if got := (GenSpec{EntrySymbol: "x"}).Entry(); got != "x" {
		t.Fatalf("declared Entry() = %q, want x", got)
	}
}

// The deterministic fixture exposes (defines + mounts) the DECLARED entry symbol,
// not a hardwired handleTime (or-3ba.4).
func TestFixtureUsesDeclaredEntrySymbol(t *testing.T) {
	dir := t.TempDir()
	if _, err := GenerateTimeServiceFixture(dir, GenSpec{Module: "orion-generated/svc", Route: "/now", Port: 8080, Format: "json", TimeZone: "UTC", EntrySymbol: "handleNow"}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	s := string(src)
	if !strings.Contains(s, "func handleNow(") {
		t.Fatalf("fixture does not define the declared entry symbol:\n%s", s)
	}
	if !strings.Contains(s, "HandlerFunc(handleNow)") {
		t.Fatalf("fixture does not mount the declared entry symbol:\n%s", s)
	}
	if strings.Contains(s, "handleTime") {
		t.Fatalf("fixture still references hardwired handleTime:\n%s", s)
	}
}
