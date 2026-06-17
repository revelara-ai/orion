package harness

import (
	"fmt"
	"math/rand"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/architect"
	"github.com/revelara-ai/orion/internal/constraints"
)

// SynthesizeOptions is the input bundle for Synthesize.
type SynthesizeOptions struct {
	// RunID is the parent run identifier; stamped on the namespace.
	RunID string

	// Model is the architectural model the workload + faults are derived
	// from.
	Model *architect.ArchitecturalModel

	// Constraints is the constraint surface; v1 reads it for shape only
	// (the workload's request distributions don't depend on constraints
	// in v1; later epics use it to favor endpoints under tighter SLOs).
	Constraints *constraints.ConstraintSurface

	// Seed deterministically governs every random choice in synthesis.
	Seed int64

	// Thoroughness selects fault intensity (fast/standard/thorough).
	// Empty defaults to standard.
	Thoroughness Thoroughness

	// Now overrides time.Now for tests; zero falls back to time.Now().UTC().
	Now time.Time
}

// Synthesize builds a Harness deterministically from opts. Two calls
// with identical opts produce byte-identical Harness values (modulo
// SynthesizedAt, which is set from opts.Now or time.Now and fixed
// per call).
func Synthesize(opts SynthesizeOptions) (*Harness, error) {
	if opts.Model == nil {
		return nil, fmt.Errorf("%w: Model required", ErrInvalidInputs)
	}
	if opts.RunID == "" {
		return nil, fmt.Errorf("%w: RunID required", ErrInvalidInputs)
	}
	thor := opts.Thoroughness
	if thor == "" {
		thor = ThoroughnessStandard
	}
	if !validThoroughness(thor) {
		return nil, fmt.Errorf("%w: bad Thoroughness %q", ErrInvalidInputs, thor)
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	rng := rand.New(rand.NewSource(opts.Seed)) //#nosec G404 -- determinism > crypto strength here
	ns := canonicalNamespace(opts.RunID)
	if !validNamespace.MatchString(ns) {
		return nil, fmt.Errorf("%w: synthesized namespace %q invalid", ErrInvalidInputs, ns)
	}

	workload := synthesizeWorkload(opts.Model, rng, thor)
	faults := synthesizeFaults(opts.Model, rng, thor)

	return &Harness{
		RunID:         opts.RunID,
		Seed:          opts.Seed,
		Thoroughness:  thor,
		Namespace:     ns,
		Workload:      workload,
		Faults:        faults,
		SynthesizedAt: now,
	}, nil
}

func validThoroughness(t Thoroughness) bool {
	switch t {
	case ThoroughnessFast, ThoroughnessStandard, ThoroughnessThorough:
		return true
	}
	return false
}

// nsSanitize collapses [^a-z0-9-] to '-' and trims leading/trailing dashes.
var nsSanitize = regexp.MustCompile(`[^a-z0-9-]+`)

// canonicalNamespace returns "orion-run-<short>" per SPEC §4.2.
// runID is sanitized to the K8s name regex; first 6 chars become the
// short id (lowercase). Empty / fully-stripped runIDs fall back to
// "anon".
func canonicalNamespace(runID string) string {
	id := strings.ToLower(runID)
	id = nsSanitize.ReplaceAllString(id, "-")
	id = strings.Trim(id, "-")
	if id == "" {
		id = "anon"
	}
	if len(id) > 6 {
		id = id[:6]
	}
	return "orion-run-" + id
}

// synthesizeWorkload builds a deterministic per-endpoint distribution
// from the model. v1: equal raw weights, jittered by rng; payload
// sizes 256-4096 bytes. Endpoints sorted by Service+Endpoint.
func synthesizeWorkload(model *architect.ArchitecturalModel, rng *rand.Rand, thor Thoroughness) WorkloadConfig {
	var dists []EndpointDist
	for _, svc := range model.Services {
		for _, ep := range svc.Endpoints {
			dists = append(dists, EndpointDist{
				Service:      svc.Name,
				Endpoint:     endpointKey(ep),
				PayloadBytes: 256 + rng.Intn(3840),
			})
		}
	}
	if len(dists) == 0 {
		dists = []EndpointDist{
			{Service: "_synthetic_", Endpoint: "_default_", PayloadBytes: 256},
		}
	}
	sort.Slice(dists, func(i, j int) bool {
		if dists[i].Service != dists[j].Service {
			return dists[i].Service < dists[j].Service
		}
		return dists[i].Endpoint < dists[j].Endpoint
	})
	for i := range dists {
		dists[i].Weight = 1.0 / float64(len(dists))
	}
	return WorkloadConfig{
		Endpoints:       dists,
		TargetRPS:       targetRPSFor(thor),
		DurationSeconds: durationFor(thor),
	}
}

func endpointKey(ep architect.Endpoint) string {
	if ep.Kind == "grpc" && ep.Service != "" {
		return ep.Service + "/" + ep.Method
	}
	return ep.Method
}

// synthesizeFaults builds per-dependency profiles. v1: latency p50
// 10-200ms scaled by thoroughness; p99 = p50 * 5; error rate 0.5%-5%
// scaled; partition probability 0.1%-1% scaled.
func synthesizeFaults(model *architect.ArchitecturalModel, rng *rand.Rand, thor Thoroughness) FaultConfig {
	scale := faultScaleFor(thor)
	seen := map[string]bool{}
	var faults []FaultProfile
	for _, svc := range model.Services {
		for _, dep := range svc.DownstreamDeps {
			if dep.TargetName == "" || seen[dep.TargetName] {
				continue
			}
			seen[dep.TargetName] = true
			p50 := int(float64(10+rng.Intn(190)) * scale)
			faults = append(faults, FaultProfile{
				TargetName:           dep.TargetName,
				LatencyP50Ms:         p50,
				LatencyP99Ms:         p50 * 5,
				ErrorRate:            (0.005 + rng.Float64()*0.045) * scale,
				PartitionProbability: (0.001 + rng.Float64()*0.009) * scale,
			})
		}
	}
	if len(faults) == 0 {
		faults = []FaultProfile{
			{TargetName: "_synthetic_", LatencyP50Ms: 50, LatencyP99Ms: 250, ErrorRate: 0.01, PartitionProbability: 0.005},
		}
	}
	sort.Slice(faults, func(i, j int) bool {
		return faults[i].TargetName < faults[j].TargetName
	})
	return FaultConfig{Faults: faults}
}

func targetRPSFor(t Thoroughness) int {
	switch t {
	case ThoroughnessFast:
		return 50
	case ThoroughnessThorough:
		return 500
	}
	return 200
}

func durationFor(t Thoroughness) int {
	switch t {
	case ThoroughnessFast:
		return 30
	case ThoroughnessThorough:
		return 180
	}
	return 60
}

func faultScaleFor(t Thoroughness) float64 {
	switch t {
	case ThoroughnessFast:
		return 0.5
	case ThoroughnessThorough:
		return 2.0
	}
	return 1.0
}
