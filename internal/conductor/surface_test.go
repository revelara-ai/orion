package conductor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/revelara-ai/orion/internal/contextengine"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

func surfaceStore(t *testing.T) (*contextstore.Store, string) {
	t.Helper()
	s, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	var pid string
	if err := s.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		pid, e = tx.Projects().Create(ctx, "demo", "build a service", "http-service")
		if e != nil {
			return e
		}
		_, e = tx.Specs().CreateDraft(ctx, pid)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	return s, pid
}

// TestSurfaceExtractionMultiFile (or-7et.5 acceptance 2): exports living
// OUTSIDE main.go are extracted — the old main.go-only channel missed them.
func TestSurfaceExtractionMultiFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "calc"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeF := func(rel, src string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeF("main.go", "package main\n\nfunc main() {}\n")
	writeF("calc/calc.go", "package calc\n\nfunc Add(a, b int) int { return a + b }\n\ntype Ledger struct{}\n")
	writeF("calc/calc_test.go", "package calc\n\nfunc TestNever() {}\n") // proof corpus: excluded

	surface := extractModuleSurface(dir, "")
	joined := strings.Join(surface, ";")
	for _, want := range []string{"func Add", "type Ledger"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("multi-file extraction must find %q, got %v", want, surface)
		}
	}
	if strings.Contains(joined, "TestNever") {
		t.Fatalf("test files are never part of the surface: %v", surface)
	}

	// Scoped extraction: only the declared dirs are read.
	scoped := extractModuleSurface(dir, "calc")
	if !strings.Contains(strings.Join(scoped, ";"), "func Add") {
		t.Fatalf("scoped extraction must read the scope dir: %v", scoped)
	}
}

// TestConformanceGateNamesMissingSymbols (or-7et.5 acceptance 1): a dependent
// that Requires a symbol its dep's ACCEPTED artifact does not export fails
// PRE-merge, with the symbol named in the error and the persisted re-grind
// feedback; a satisfied manifest passes; no declared Requires skips the gate.
func TestConformanceGateNamesMissingSymbols(t *testing.T) {
	ctx := context.Background()
	store, pid := surfaceStore(t)
	var mu sync.Mutex

	persistModuleSurface(ctx, store, &mu, pid, "a1", []string{"func Add", "route /sum"})
	if err := store.SaveStringListKind(ctx, pid, contextstore.ModuleRequiresKind+"b1", []string{"Add", "Note"}); err != nil {
		t.Fatal(err)
	}
	tasks := []orchestrator.PlanTask{{ID: "a1"}, {ID: "b1", DependsOn: []string{"a1"}}}
	gate := conformanceGate(ctx, store, pid, tasks, nil)

	err := gate(decomposer.TaskCluster{Key: "clB", Members: []string{"b1"}})
	if err == nil {
		t.Fatal("a missing required symbol must fail the pre-merge gate")
	}
	if !strings.Contains(err.Error(), "Note") || strings.Contains(err.Error(), `"Add"`) {
		t.Fatalf("the gate must name exactly the missing symbol: %v", err)
	}
	var esc []contextstore.Escalation
	_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		esc, e = tx.Escalations().ListOpen(ctx)
		return e
	})
	var found bool
	for _, e := range esc {
		if strings.Contains(e.Detail, "Note") && strings.Contains(e.Detail, "re-grind") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the re-grind feedback must persist, naming the symbol: %+v", esc)
	}

	// Satisfy the manifest → gate passes.
	persistModuleSurface(ctx, store, &mu, pid, "a1", []string{"func Add", "func Note"})
	if err := gate(decomposer.TaskCluster{Key: "clB", Members: []string{"b1"}}); err != nil {
		t.Fatalf("a satisfied manifest must pass: %v", err)
	}

	// At-rest (acceptance 4): no declared Requires → the gate skips entirely.
	if err := gate(decomposer.TaskCluster{Key: "clA", Members: []string{"a1"}}); err != nil {
		t.Fatalf("no declared Requires must skip the gate: %v", err)
	}
}

// TestDepSurfaceInjectionAtScale (or-7et.5 acceptance 3): with far more
// modules than the recall window and memory caps could carry, every direct
// dep's surface is STILL in the rendered constraints — deterministic lookup,
// not heat-ranked recall.
func TestDepSurfaceInjectionAtScale(t *testing.T) {
	ctx := context.Background()
	store, pid := surfaceStore(t)
	var mu sync.Mutex

	var deps []string
	for i := 0; i < 30; i++ { // > the 12-item recall window and any heat ranking
		key := string(rune('a'+i%26)) + "-mod-" + strings.Repeat("x", i%5)
		deps = append(deps, key)
		persistModuleSurface(ctx, store, &mu, pid, key, []string{"func Export" + key})
	}
	var b contextengine.Bundle
	injectDepSurfaces(ctx, store, pid, deps, &b)
	for _, dep := range deps {
		if !b.HasConstraint("func Export" + dep) {
			t.Fatalf("dependency %s's surface must be an always-injected constraint", dep)
		}
	}

	// A dep with no accepted surface injects nothing (and never blocks).
	var empty contextengine.Bundle
	injectDepSurfaces(ctx, store, pid, []string{"never-built"}, &empty)
	if len(empty.Constraints) != 0 {
		t.Fatalf("an unbuilt dep must inject nothing, got %v", empty.Constraints)
	}
}
