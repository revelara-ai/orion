package constraints

import (
	"context"
	"fmt"

	"github.com/revelara-ai/orion/internal/architect"
	"github.com/revelara-ai/orion/internal/polaris"
)

// Inferer combines a snapshotted Polaris controls catalog with code-
// derived implicit constraints to produce a ConstraintSurface.
type Inferer struct{}

// NewInferer constructs an Inferer.
func NewInferer() *Inferer { return &Inferer{} }

// InferOptions is the per-call input to Inferer.Infer.
type InferOptions struct {
	// Model is the architectural model from the architect package.
	// Required.
	Model *architect.ArchitecturalModel

	// Catalog is the snapshotted Polaris controls catalog. Required.
	// Inferer.Infer DOES NOT make live Polaris calls; the caller is
	// responsible for fetching + pinning the catalog.
	Catalog *polaris.ControlsCatalog
}

// Infer runs the implicit-constraint extraction pass and returns a
// ConstraintSurface. The surface contains the catalog (for explicit
// constraints) and the implicit constraints; downstream consumers use
// ConstraintSurface.Resolve to pick the authoritative binding.
func (i *Inferer) Infer(_ context.Context, opts InferOptions) (*ConstraintSurface, error) {
	if opts.Model == nil {
		return nil, fmt.Errorf("%w: Model is nil", ErrInvalidOptions)
	}
	if opts.Catalog == nil {
		return nil, fmt.Errorf("%w: Catalog is nil", ErrInvalidOptions)
	}

	surface := &ConstraintSurface{
		CatalogSnapshotAt: opts.Catalog.SnapshotAt,
		SnapshotControls:  opts.Catalog.Controls,
	}

	for _, svc := range opts.Model.Services {
		if svc.SourceDir == "" {
			continue
		}
		ic := extractImplicitConstraints(opts.Model.Repo, svc)
		surface.ImplicitConstraints = append(surface.ImplicitConstraints, ic...)
	}

	return surface, nil
}
