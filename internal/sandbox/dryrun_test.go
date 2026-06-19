package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDryRunPredictsWithoutMutating: a dry-run write returns a prediction (effect
// + blast radius) and leaves the filesystem untouched; the real run writes.
func TestDryRunPredictsWithoutMutating(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{"main.go": "package main\nfunc main(){}\n", "go.mod": "module x\n"}
	target := filepath.Join(dir, "main.go")

	// Dry run: predicts, does not mutate.
	pred, err := PrepareArtifact(dir, files, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if pred.Mutated {
		t.Fatal("dry run reported Mutated=true")
	}
	if !strings.Contains(pred.Effect, "would write") || pred.BlastRadius == "" {
		t.Fatalf("dry run missing predicted effect/blast radius: %+v", pred)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("dry run mutated the filesystem (file exists): %v", err)
	}

	// Real run: writes.
	pred, err = PrepareArtifact(dir, files, false)
	if err != nil {
		t.Fatalf("real run: %v", err)
	}
	if !pred.Mutated {
		t.Fatal("real run reported Mutated=false")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("real run did not write the file: %v", err)
	}
}
