package constraints_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/revelara-ai/orion/internal/architect"
	"github.com/revelara-ai/orion/internal/constraints"
	"github.com/revelara-ai/orion/internal/polaris"
)

func fixtureRepo(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(here), "testdata", "tiny_demo")
}

func sampleCatalog() *polaris.ControlsCatalog {
	return &polaris.ControlsCatalog{
		SnapshotAt: "2026-05-10T00:00:00Z",
		Total:      3,
		Controls: []polaris.Control{
			{ControlCode: "RC-001", Name: "Outbound timeout", Category: "fault_tolerance", Type: "preventive", Weight: 3},
			{ControlCode: "RC-002", Name: "Retry with jitter", Category: "fault_tolerance", Type: "preventive", Weight: 2},
			{ControlCode: "RC-018", Name: "Idempotency key", Category: "change_management", Type: "preventive", Weight: 3},
		},
	}
}

func TestInferer_RequiresArchitecturalModel(t *testing.T) {
	inf := constraints.NewInferer()
	_, err := inf.Infer(context.Background(), constraints.InferOptions{
		Catalog: sampleCatalog(),
	})
	if err == nil {
		t.Fatal("want error on missing model")
	}
	if !errors.Is(err, constraints.ErrInvalidOptions) {
		t.Errorf("err=%v; want ErrInvalidOptions", err)
	}
}

func TestInferer_RequiresCatalog(t *testing.T) {
	inf := constraints.NewInferer()
	_, err := inf.Infer(context.Background(), constraints.InferOptions{
		Model: &architect.ArchitecturalModel{Repo: "/tmp"},
	})
	if err == nil {
		t.Fatal("want error on missing catalog")
	}
	if !errors.Is(err, constraints.ErrInvalidOptions) {
		t.Errorf("err=%v; want ErrInvalidOptions", err)
	}
}

func TestInferer_IncludesAllCatalogControls(t *testing.T) {
	model := &architect.ArchitecturalModel{
		Repo:     "/tmp",
		Services: []architect.Service{{Name: "svc-a"}},
	}
	cat := sampleCatalog()

	inf := constraints.NewInferer()
	surface, err := inf.Infer(context.Background(), constraints.InferOptions{
		Model:   model,
		Catalog: cat,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(surface.SnapshotControls) != len(cat.Controls) {
		t.Errorf("SnapshotControls=%d; want %d", len(surface.SnapshotControls), len(cat.Controls))
	}
	if surface.CatalogSnapshotAt != cat.SnapshotAt {
		t.Errorf("CatalogSnapshotAt=%q; want %q", surface.CatalogSnapshotAt, cat.SnapshotAt)
	}
}

func TestInferer_ImplicitConstraintsFromGoTimeout(t *testing.T) {
	repo := fixtureRepo(t)
	model := &architect.ArchitecturalModel{
		Repo: repo,
		Services: []architect.Service{
			{Name: "svc-with-timeout", Language: "go", SourceDir: "src/svc-with-timeout"},
			{Name: "svc-no-timeout", Language: "go", SourceDir: "src/svc-no-timeout"},
		},
	}
	inf := constraints.NewInferer()
	surface, err := inf.Infer(context.Background(), constraints.InferOptions{
		Model:   model,
		Catalog: sampleCatalog(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Expect at least one implicit timeout-budget constraint from
	// src/svc-with-timeout/main.go (which uses context.WithTimeout).
	var timeoutConstraints int
	for _, ic := range surface.ImplicitConstraints {
		if ic.Kind == constraints.KindTimeoutBudget &&
			ic.Service == "svc-with-timeout" {
			timeoutConstraints++
		}
	}
	if timeoutConstraints == 0 {
		t.Errorf("no implicit timeout-budget constraint detected for svc-with-timeout; got %d total implicit constraints",
			len(surface.ImplicitConstraints))
	}
}

func TestInferer_ConflictResolution_PolarisExplicitWins(t *testing.T) {
	model := &architect.ArchitecturalModel{
		Repo:     "/tmp",
		Services: []architect.Service{{Name: "svc-a", Language: "go", SourceDir: "src/svc-a"}},
	}
	cat := sampleCatalog()

	inf := constraints.NewInferer()
	surface, err := inf.Infer(context.Background(), constraints.InferOptions{
		Model:   model,
		Catalog: cat,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Construct a contrived conflict: an implicit constraint and an
	// explicit Polaris control that both bind the same (service, kind).
	implicit := constraints.ImplicitConstraint{
		Kind:    constraints.KindTimeoutBudget,
		Service: "svc-a",
	}
	explicitCode := "RC-001" // fault_tolerance / outbound timeout

	resolved := surface.Resolve(implicit, explicitCode)
	if resolved.Provenance != constraints.ProvenanceExplicit {
		t.Errorf("Resolve.Provenance=%q; want explicit (Polaris should win)", resolved.Provenance)
	}
	if resolved.ControlCode != explicitCode {
		t.Errorf("Resolve.ControlCode=%q; want %q", resolved.ControlCode, explicitCode)
	}
}

func TestInferer_ResolveFallsBackToImplicit(t *testing.T) {
	surface := &constraints.ConstraintSurface{}
	implicit := constraints.ImplicitConstraint{Kind: constraints.KindTimeoutBudget, Service: "x"}

	resolved := surface.Resolve(implicit, "")
	if resolved.Provenance != constraints.ProvenanceImplicit {
		t.Errorf("Resolve.Provenance=%q; want implicit", resolved.Provenance)
	}
}

// Helper to set up testdata fixture if missing.
func TestMain(m *testing.M) {
	_, here, _, _ := runtime.Caller(0)
	td := filepath.Join(filepath.Dir(here), "testdata", "tiny_demo")
	_ = os.MkdirAll(filepath.Join(td, "src", "svc-with-timeout"), 0o755)
	_ = os.MkdirAll(filepath.Join(td, "src", "svc-no-timeout"), 0o755)
	_ = os.WriteFile(
		filepath.Join(td, "src", "svc-with-timeout", "main.go"),
		[]byte(`package main

import (
	"context"
	"time"
)

func call(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = ctx
}
`),
		0o644,
	)
	_ = os.WriteFile(
		filepath.Join(td, "src", "svc-no-timeout", "main.go"),
		[]byte(`package main

import "context"

func call(ctx context.Context) { _ = ctx }
`),
		0o644,
	)
	os.Exit(m.Run())
}
