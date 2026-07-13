package completeness

import (
	"os"
	"path/filepath"
	"testing"
)

// TestChecklistHonorsExternalConfig (or-kzf.2 DONE-WHEN): editing
// checklists.yaml changes the completeness gate's required decisions without
// a rebuild; other project types keep their compiled defaults; an invalid
// file falls back to the compiled registry.
func TestChecklistHonorsExternalConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORION_HARNESS_DIR", dir)

	compiled := checklistFor("http-service")
	if len(compiled) == 0 {
		t.Fatal("compiled defaults must exist")
	}

	if err := os.WriteFile(filepath.Join(dir, "checklists.yaml"), []byte(`
functional:
  http-service:
    - {key: auth_model, dimension: security, question: "What is the auth model?", fallback: "none"}
universal:
  - {key: slo_targets, dimension: slo, question: "What SLO?", fallback: "tier default"}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	got := checklistFor("http-service")
	if len(got) != 2 {
		t.Fatalf("external config must replace both lists (1 functional + 1 universal), got %d: %+v", len(got), got)
	}
	if got[0].Key != "auth_model" || got[0].Dimension != DimSecurity {
		t.Fatalf("functional override lost: %+v", got[0])
	}
	if got[1].Key != "slo_targets" || got[1].Dimension != DimSLO {
		t.Fatalf("universal override lost: %+v", got[1])
	}

	// An unconfigured type keeps compiled behavior (no functional decisions,
	// but the overridden universal list applies).
	other := checklistFor("grpc-service")
	if len(other) != 1 || other[0].Key != "slo_targets" {
		t.Fatalf("unconfigured type: compiled functional (none) + external universal, got %+v", other)
	}

	// Invalid file → compiled registry, never a partial apply.
	if err := os.WriteFile(filepath.Join(dir, "checklists.yaml"), []byte("universal: [{key: x, dimension: bogus, question: q}]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := checklistFor("http-service"); len(got) != len(compiled) {
		t.Fatalf("invalid config must fall back to the full compiled checklist, got %d want %d", len(got), len(compiled))
	}
}
