package conductor

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBrownfieldIntakeFromTarget proves the or-any.8 wiring: no target stays
// greenfield; a target with existing source becomes a brownfield clone of it; an
// empty target dir falls back to greenfield.
func TestBrownfieldIntakeFromTarget(t *testing.T) {
	if got := brownfieldIntake(""); got.Brownfield {
		t.Fatalf("empty target must be greenfield, got %+v", got)
	}

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "app.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := brownfieldIntake(src)
	if !got.Brownfield || got.Source != src {
		t.Fatalf("target with source must be a brownfield intake of it, got %+v", got)
	}

	if got := brownfieldIntake(t.TempDir()); got.Brownfield {
		t.Fatalf("empty dir must classify greenfield, got %+v", got)
	}
}
