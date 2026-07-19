package spec

import (
	"testing"
)

// TestCompileSplitsLanguageRuntime (or-4y7.10): the compiled spec stores the
// BASE language under direction.language and the version pin under
// direction.runtime; an explicit runtime answer is never overwritten.
func TestCompileSplitsLanguageRuntime(t *testing.T) {
	d := normalizeDirectionDecisions(map[string]string{"direction.language": "python 3.12"})
	if d["direction.language"] != "python" || d["direction.runtime"] != "3.12" {
		t.Fatalf("versioned answer must split, got %+v", d)
	}
	d = normalizeDirectionDecisions(map[string]string{"direction.language": "Python@3.11", "direction.runtime": "3.10"})
	if d["direction.runtime"] != "3.10" {
		t.Fatalf("an explicit direction.runtime must never be overwritten, got %+v", d)
	}
	d = normalizeDirectionDecisions(map[string]string{"direction.language": "go"})
	if d["direction.language"] != "go" || d["direction.runtime"] != "" {
		t.Fatalf("a bare language stays as-is, got %+v", d)
	}
}
