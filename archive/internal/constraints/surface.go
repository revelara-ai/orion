// Package constraints derives the SLO-Fabric / ConstraintSurface for a
// run. Per SPEC §12.2, it combines two sources:
//
//   - Snapshotted Polaris controls catalog (explicit constraints).
//   - Code-derived implicit constraints (existing context.WithTimeout
//     calls become assumed budgets, existing retry config becomes
//     assumed error rates, etc.).
//
// Conflict resolution: Polaris explicit > code-inferred. The
// ConstraintSurface preserves both with provenance flags so that
// downstream consumers (verifier, harness synthesizer) can audit
// where each constraint came from.
//
// Snapshot discipline (SPEC §14.6): Inferer.Infer takes a
// ControlsCatalog already-fetched by the caller; never makes live
// Polaris calls inside Infer. The Inferer's input is in-memory snapshot
// data; the output is in-memory snapshot data; downstream consumers
// hold the surface through the run's lifetime.
package constraints

import (
	"errors"

	"github.com/revelara-ai/orion/internal/polaris"
)

// Sentinel errors for callers to errors.Is against.
var (
	// ErrInvalidOptions: caller passed bad InferOptions (nil model or
	// nil catalog).
	ErrInvalidOptions = errors.New("constraints: invalid infer options")
)

// ConstraintKind enumerates the recognized constraint flavors. Adding
// a new kind is a controlled change; downstream consumers switch on
// these.
type ConstraintKind string

const (
	// KindTimeoutBudget caps per-call latency the caller assumes
	// (typically derived from context.WithTimeout in the source).
	KindTimeoutBudget ConstraintKind = "timeout_budget"

	// KindRetryHygiene constrains retry behavior (max attempts, backoff,
	// jitter). v1 detects only the presence/absence of a retry loop;
	// richer parsing comes later.
	KindRetryHygiene ConstraintKind = "retry_hygiene"

	// KindIdempotencyKey requires idempotency-key handling for
	// state-mutating endpoints.
	KindIdempotencyKey ConstraintKind = "idempotency_key"
)

// Provenance flags where a constraint binding came from. Used by the
// Resolve helper and by downstream consumers when reporting.
type Provenance string

const (
	// ProvenanceExplicit means an explicit Polaris control is the
	// source. Always wins on conflict.
	ProvenanceExplicit Provenance = "explicit"

	// ProvenanceImplicit means a code-derived inference is the source.
	// Loses on conflict with an explicit binding.
	ProvenanceImplicit Provenance = "implicit"
)

// ImplicitConstraint is one constraint inferred from source code (v1:
// from Go context.WithTimeout calls; future v1.x: more languages and
// patterns).
type ImplicitConstraint struct {
	Kind ConstraintKind `json:"kind"`

	// Service is the application service the constraint binds to (from
	// ArchitecturalModel.Service.Name).
	Service string `json:"service"`

	// SourceFile is the file (relative to repo root) the constraint was
	// inferred from.
	SourceFile string `json:"source_file,omitempty"`

	// Line is the 1-indexed line number in SourceFile.
	Line int `json:"line,omitempty"`

	// Value is the inferred numeric value (if any). For timeout budgets,
	// the duration in milliseconds. Zero if not numeric or not parseable.
	ValueMillis int `json:"value_millis,omitempty"`

	// RawSnippet is the matched source text, capped at 200 chars.
	RawSnippet string `json:"raw_snippet,omitempty"`
}

// ConstraintSurface is the per-run output of the inferer. Consumed by
// the verifier (E1-7) to gate patch acceptance and by the patch
// synthesizer (E1-6) to know what constraints to honor.
type ConstraintSurface struct {
	// CatalogSnapshotAt is the timestamp from the input catalog. Pinned
	// here so downstream consumers can verify they're using the right
	// snapshot.
	CatalogSnapshotAt string `json:"catalog_snapshot_at"`

	// SnapshotControls is the controls catalog at run-start. Indexed by
	// ControlCode for downstream lookups.
	SnapshotControls []polaris.Control `json:"snapshot_controls"`

	// ImplicitConstraints are code-inferred bindings.
	ImplicitConstraints []ImplicitConstraint `json:"implicit_constraints"`
}

// ResolvedConstraint is the output of ConstraintSurface.Resolve: the
// authoritative binding (explicit or implicit) that downstream
// consumers should treat as the source of truth.
type ResolvedConstraint struct {
	Provenance  Provenance          `json:"provenance"`
	Kind        ConstraintKind      `json:"kind"`
	Service     string              `json:"service"`
	ControlCode string              `json:"control_code,omitempty"` // set when Provenance == explicit
	Implicit    *ImplicitConstraint `json:"implicit,omitempty"`     // set when Provenance == implicit
}

// Resolve picks between an implicit and a (potentially-empty) explicit
// binding for the same (Kind, Service). Polaris-explicit always wins
// when explicitCode is non-empty AND the catalog contains it.
func (s *ConstraintSurface) Resolve(implicit ImplicitConstraint, explicitCode string) ResolvedConstraint {
	if explicitCode != "" {
		for i := range s.SnapshotControls {
			if s.SnapshotControls[i].ControlCode == explicitCode {
				return ResolvedConstraint{
					Provenance:  ProvenanceExplicit,
					Kind:        implicit.Kind,
					Service:     implicit.Service,
					ControlCode: explicitCode,
				}
			}
		}
		// Explicit code given but not in catalog: still prefer it as a
		// directive but mark provenance as explicit; consumers may
		// treat the control as "expected but absent."
		return ResolvedConstraint{
			Provenance:  ProvenanceExplicit,
			Kind:        implicit.Kind,
			Service:     implicit.Service,
			ControlCode: explicitCode,
		}
	}

	implicit2 := implicit
	return ResolvedConstraint{
		Provenance: ProvenanceImplicit,
		Kind:       implicit.Kind,
		Service:    implicit.Service,
		Implicit:   &implicit2,
	}
}
