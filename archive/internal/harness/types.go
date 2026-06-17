// Package harness synthesizes a deterministic verification harness for
// one Orion run per SPEC §12.4.
//
// The Synthesizer takes an ArchitecturalModel + ConstraintSurface +
// per-run seed and returns a Harness containing a workload spec
// (per-endpoint request distributions) and a fault spec (per-dependency
// latency / error / partition profiles). With the same seed, two
// Synthesize calls produce byte-identical Harness JSON; this is the
// SPEC §12.4 determinism contract that the verifier (E1-7) relies on
// when comparing baseline and patched runs.
//
// v1 ships:
//   - Deterministic synthesis from seed (workload + fault).
//   - In-process Runner that returns synthetic Metrics from harness.Seed.
//     Used by the verifier's wireup tests; the verifier calls Run once
//     for baseline, once per patch.
//   - K8s NetworkPolicy + namespace template for the materialization
//     plan. The plan is structured (MaterializationPlan) so the
//     orchestrator (E1-8) can emit it; live `kubectl apply` materialization
//     and toxiproxy / testcontainers wiring is deferred to a follow-up
//     issue tracked in beads (see notes on orion-sfp).
package harness

import (
	"errors"
	"regexp"
	"time"
)

// Sentinel errors.
var (
	// ErrInvalidInputs: required input field missing or unsafe.
	ErrInvalidInputs = errors.New("harness: invalid inputs")

	// ErrMaterialization: the materialization plan or stub runner failed.
	ErrMaterialization = errors.New("harness: materialization failed")
)

// Thoroughness selects fault-injection intensity. Maps to per-tier
// trial counts the verifier honors (SPEC §12.6).
type Thoroughness string

// Tiers for Thoroughness.
const (
	ThoroughnessFast     Thoroughness = "fast"
	ThoroughnessStandard Thoroughness = "standard"
	ThoroughnessThorough Thoroughness = "thorough"
)

// MaterializerMode selects between the in-process (E1) runner and the
// live K8s materializer (E4-6). The worker switches at startup based
// on the K8S_HARNESS_ENABLED env var.
type MaterializerMode string

// Modes.
const (
	MaterializerLocal MaterializerMode = "local"
	MaterializerK8s   MaterializerMode = "k8s"
)

// EndpointDist is the synthesized request-distribution shape for one
// endpoint. v1 uses request weights (relative); the runner translates
// those into RPS at execution time.
type EndpointDist struct {
	// Service is the service name the endpoint belongs to.
	Service string `json:"service"`

	// Endpoint is the canonical method+route key (e.g. "POST /cart").
	Endpoint string `json:"endpoint"`

	// Weight is the relative traffic share for this endpoint within the
	// workload, normalized so all weights sum to 1.0.
	Weight float64 `json:"weight"`

	// PayloadBytes is the synthesized request body size used as a
	// realistic-but-cheap stand-in.
	PayloadBytes int `json:"payload_bytes"`
}

// FaultProfile is the synthesized fault configuration for one
// downstream dependency.
type FaultProfile struct {
	// TargetName is the dependency name (service or store).
	TargetName string `json:"target_name"`

	// LatencyP50Ms is the median injected latency for this dependency
	// during the run.
	LatencyP50Ms int `json:"latency_p50_ms"`

	// LatencyP99Ms is the 99th-percentile injected latency.
	LatencyP99Ms int `json:"latency_p99_ms"`

	// ErrorRate is the [0.0, 1.0] probability of returning an error
	// from this dependency.
	ErrorRate float64 `json:"error_rate"`

	// PartitionProbability is the [0.0, 1.0] probability of injecting a
	// network partition (no response, hard failure) instead of an error.
	PartitionProbability float64 `json:"partition_probability"`
}

// WorkloadConfig is the synthesized workload spec for a Harness.
type WorkloadConfig struct {
	// Endpoints lists the per-endpoint distributions. Sorted by Service+
	// Endpoint for determinism.
	Endpoints []EndpointDist `json:"endpoints"`

	// TargetRPS is the aggregate target request rate the workload aims
	// to sustain.
	TargetRPS int `json:"target_rps"`

	// DurationSeconds is the workload runtime per trial.
	DurationSeconds int `json:"duration_seconds"`
}

// FaultConfig is the synthesized fault spec for a Harness.
type FaultConfig struct {
	// Faults lists the per-dependency profiles. Sorted by TargetName.
	Faults []FaultProfile `json:"faults"`
}

// Harness is the immutable per-run synthesis output. Marshals to a
// stable JSON shape; two calls to Synthesize with identical inputs
// (model, constraints, seed, thoroughness) produce byte-identical
// JSON. The verifier consumes this contract.
type Harness struct {
	// RunID is the parent run identifier. Stamped on the materialized
	// namespace name.
	RunID string `json:"run_id"`

	// Seed is the deterministic seed used for synthesis. Recorded so
	// the verifier and the reproduction bundle can replay.
	Seed int64 `json:"seed"`

	// Thoroughness governs trial-count budget; the synthesizer scales
	// fault intensity by tier.
	Thoroughness Thoroughness `json:"thoroughness"`

	// Namespace is the K8s namespace name the materializer would create.
	// Format: "orion-run-<runIDShort>" per SPEC §4.2 sanitized.
	Namespace string `json:"namespace"`

	// Workload is the synthesized workload spec.
	Workload WorkloadConfig `json:"workload"`

	// Faults is the synthesized fault spec.
	Faults FaultConfig `json:"faults"`

	// SynthesizedAt is the wall-clock RFC3339 timestamp at which Synthesize
	// completed.
	SynthesizedAt time.Time `json:"synthesized_at"`
}

// Metrics is the per-run output of Runner.Run, consumed by the
// verifier (E1-7). Names align with SPEC §12.6 axes.
type Metrics struct {
	// LatencyP99Ms is the 99th-percentile end-to-end latency the harness
	// observed.
	LatencyP99Ms int `json:"latency_p99_ms"`

	// ErrorRate is the [0.0, 1.0] error rate observed.
	ErrorRate float64 `json:"error_rate"`

	// CascadeProbability is the [0.0, 1.0] probability that a single
	// dependency fault cascaded into a multi-service failure during the
	// trial. Heuristic for system-level fragility.
	CascadeProbability float64 `json:"cascade_probability"`

	// PeakMemoryBytes is the peak RSS observed during the trial.
	PeakMemoryBytes int64 `json:"peak_memory_bytes"`

	// TrialSeed is the per-trial seed; recorded so reruns can reproduce.
	TrialSeed int64 `json:"trial_seed"`
}

// validNamespace enforces the K8s name regex (lowercase + dashes).
var validNamespace = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
