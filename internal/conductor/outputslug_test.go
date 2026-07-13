package conductor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
)

// TestServiceOutputDirNonHTTPSlug (or-045a.7 DONE-WHEN d): a non-HTTP spec
// (no route) derives its output slug from the INTENT, not the meaningless
// route — and the HTTP path keeps its route-derived slug byte-identically.
func TestServiceOutputDirNonHTTPSlug(t *testing.T) {
	game := spec.ExecutableSpec{Intent: "Build a PvE game like Arc Raiders with RL-driven mechs"}
	got := ServiceOutputDir("/out", game)
	base := filepath.Base(got)
	if strings.Contains(base, "service") || base == "" {
		t.Fatalf("a non-HTTP slug must not be the route-derived '*-service' default, got %q", base)
	}
	if !strings.Contains(base, "build-a-pve-game") {
		t.Fatalf("the slug must derive from the intent, got %q", base)
	}
	// Negative: an HTTP spec keeps the route-derived slug.
	http := spec.ExecutableSpec{}
	http.ResponseContract.Route = "/time"
	if filepath.Base(ServiceOutputDir("/out", http)) != "time-service" {
		t.Fatalf("the HTTP slug must stay route-derived, got %q", ServiceOutputDir("/out", http))
	}
	// A spec with neither route nor intent still yields a stable non-empty leaf.
	if filepath.Base(ServiceOutputDir("/out", spec.ExecutableSpec{})) == "" {
		t.Fatal("the slug must never be empty")
	}
}

// TestExportProjectDocs (or-045a.7 DONE-WHEN c): the ratified goals + hazard
// model export into the target repo's docs/ with provenance (hash) — the
// supported surface for what the dogfood free-wrote into the harness cwd.
func TestExportProjectDocs(t *testing.T) {
	dest := t.TempDir()
	goals := orchestrator.GoalsDoc{Goals: []string{"uncanny RL mech movement"}, NonGoals: []string{"PvP"}}
	model := stpa.Model{Losses: []stpa.Loss{{ID: "GL1", Description: "players lose trust"}}}
	written, err := ExportProjectDocs(dest, goals, "abc123hash", &model)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) < 2 {
		t.Fatalf("expected goals + hazards docs, wrote %v", written)
	}
	gb, err := os.ReadFile(filepath.Join(dest, "docs", "goals.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gb), "uncanny RL mech movement") || !strings.Contains(string(gb), "abc123hash") {
		t.Fatalf("goals.md must carry the content and the anchor hash provenance:\n%s", gb)
	}
	hb, err := os.ReadFile(filepath.Join(dest, "docs", "hazards.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hb), "players lose trust") {
		t.Fatalf("hazards.md must carry the losses:\n%s", hb)
	}
	// Negative: no model → only goals export, no empty hazards file.
	dest2 := t.TempDir()
	if _, err := ExportProjectDocs(dest2, goals, "h", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest2, "docs", "hazards.md")); !os.IsNotExist(err) {
		t.Fatal("no hazard model must mean no hazards.md")
	}
}
