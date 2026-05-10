// Package architect performs brownfield architectural inference: given a
// repo path plus rvl-cli scanner findings, produce an ArchitecturalModel
// describing the services, endpoints, downstream dependencies, and
// envelope_confidence per SPEC §12.1.
//
// The inference combines two passes:
//
//   - Structural pass (deterministic): walks Kubernetes manifests, helm
//     chart templates, and .proto files to discover services and gRPC
//     RPCs. This pass is reproducible byte-for-byte across runs.
//   - LLM pass (best-effort): for each discovered service, asks an LLM
//     to enumerate HTTP endpoints and downstream client calls from the
//     service's source files. LLM output is not byte-deterministic; we
//     assert structural equivalence (same services, same endpoint count
//     order-of-magnitude) instead.
//
// envelope_confidence is computed from coverage signals (§12.1) and
// surfaced to downstream consumers (verifier, harness synthesizer) so
// they can gate on inference quality.
package architect

// ArchitecturalModel is the per-run inference output. Mirrors the JSONB
// shape described in SPEC §4.1.4. Field order is stable for golden-file
// comparisons.
type ArchitecturalModel struct {
	// Repo is the absolute path the model was inferred from.
	Repo string `json:"repo"`

	// CommitSHA is the pinned commit at which inference ran. Empty if
	// the caller didn't supply one.
	CommitSHA string `json:"commit_sha,omitempty"`

	// Services are the application services discovered in the repo.
	// Sorted by Name for determinism.
	Services []Service `json:"services"`

	// PersistentStores are databases, queues, caches discovered.
	PersistentStores []PersistentStore `json:"persistent_stores,omitempty"`

	// HotPaths are cross-service invocation chains marked as
	// performance-critical (currently inferred only when the LLM pass
	// surfaces them).
	HotPaths []HotPath `json:"hot_paths,omitempty"`

	// EnvelopeConfidence is the 0-1 quality score for this model.
	// Below `envelope_confidence_floor` (default 0.4 per SPEC §5.1)
	// blocks downstream synthesis and emits an escalation.
	EnvelopeConfidence EnvelopeConfidence `json:"envelope_confidence"`
}

// Service is one application service discovered in the repo.
type Service struct {
	// Name is the canonical service name (e.g., "checkoutservice").
	// Stable across runs; sourced from k8s manifests when present.
	Name string `json:"name"`

	// Language is the primary implementation language (e.g., "go",
	// "python", "java"). Empty if undetermined.
	Language string `json:"language,omitempty"`

	// SourceDir is the in-repo path to the service source (e.g.,
	// "src/checkoutservice"). Empty if not discoverable.
	SourceDir string `json:"source_dir,omitempty"`

	// Endpoints are gRPC RPCs and HTTP routes the service exposes.
	// gRPC RPCs come from .proto files (deterministic). HTTP endpoints
	// come from the LLM pass (best-effort).
	Endpoints []Endpoint `json:"endpoints,omitempty"`

	// DownstreamDeps are services or persistent stores this service
	// calls. helm-chart-declared service-to-service deps are
	// deterministic; LLM-traced calls are best-effort.
	DownstreamDeps []DownstreamDep `json:"downstream_deps,omitempty"`
}

// Endpoint represents one externally-callable surface of a Service.
type Endpoint struct {
	// Kind is "grpc" or "http".
	Kind string `json:"kind"`

	// Method is the gRPC method name (e.g., "PlaceOrder") or HTTP
	// method+path (e.g., "POST /cart/checkout").
	Method string `json:"method"`

	// Service is the gRPC service name (e.g., "CheckoutService") for
	// gRPC kind; empty for HTTP.
	Service string `json:"service,omitempty"`

	// SourceFile is the file the endpoint was discovered in (relative
	// to repo root); useful for traceability.
	SourceFile string `json:"source_file,omitempty"`

	// SourceProvenance is "structural" (proto/manifest) or "llm".
	SourceProvenance string `json:"source_provenance"`
}

// DownstreamDep is one outbound dependency the Service has on another
// service or store.
type DownstreamDep struct {
	// TargetName is the named target (service name or store name).
	TargetName string `json:"target_name"`

	// Kind is "service" or "store".
	Kind string `json:"kind"`

	// Protocol is "grpc", "http", "sql", "redis", "kafka", "amqp", etc.
	Protocol string `json:"protocol,omitempty"`

	// SourceProvenance is "structural" (helm-declared, env var injection)
	// or "llm".
	SourceProvenance string `json:"source_provenance"`
}

// PersistentStore is a database, queue, or cache discovered in manifests.
type PersistentStore struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"` // "postgres" | "redis" | "kafka" | ...
	Provider string `json:"provider,omitempty"`
}

// HotPath is a cross-service request path the inferer believes is
// high-frequency or latency-critical.
type HotPath struct {
	Description string   `json:"description"`
	Services    []string `json:"services"`
}

// EnvelopeConfidence is the model's self-reported quality score.
type EnvelopeConfidence struct {
	// Score is 0-1, where 0.0 means "no useful signal" and 1.0 means
	// "every service has at least one endpoint and downstream dep
	// traced from at least one source." Computed in
	// envelope_confidence.go.
	Score float64 `json:"score"`

	// ServiceCoverage is the fraction of services with at least one
	// endpoint discovered.
	ServiceCoverage float64 `json:"service_coverage"`

	// EndpointCoverage is the fraction of services with at least one
	// endpoint from any source.
	EndpointCoverage float64 `json:"endpoint_coverage"`

	// DependencyCoverage is the fraction of services with at least one
	// downstream dep traced.
	DependencyCoverage float64 `json:"dependency_coverage"`
}
