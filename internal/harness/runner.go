package harness

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"math"
	"math/rand"
)

// Runner executes a Harness against a target system and returns
// observed Metrics. The verifier (E1-7) calls Runner.Run once for the
// baseline workspace and once per CandidatePatch.
//
// In v1, Orion ships InProcessRunner: a deterministic runner that
// derives synthetic Metrics from the harness seed plus a per-trial
// salt. This proves the contract end-to-end so the verifier can be
// built and tested before the K8s + testcontainers materialization
// (deferred to a follow-up issue) lands. The signature is the
// production one; the K8s runner will be a drop-in replacement.
type Runner interface {
	Run(ctx context.Context, h *Harness, trialSalt int64) (*Metrics, error)
}

// InProcessRunner is the v1 deterministic Runner. It does NOT call out
// to any system; it derives Metrics from the harness's structure plus
// the trial salt. The intent is to give the verifier a stable,
// reproducible signal source while the K8s materialization is still
// being built.
//
// Because the metrics are derived (not measured), this Runner is NOT
// suitable for production verification. It is gated behind the same
// InProcessRunner type so callers (the verifier wireup tests) opt in
// explicitly.
type InProcessRunner struct {
	// PatchedDelta represents how much "better" a hypothetical patched
	// system would do vs baseline (0.0 = same as baseline, positive =
	// improvement). The verifier uses this to test that its
	// "patched dominates baseline" decision logic actually fires; the
	// real Runner derives this from measurement.
	PatchedDelta float64
}

// Run derives Metrics from the Harness + trialSalt deterministically.
// The mapping is intentionally simple but stable: latencyP99 scales
// with the median injected latency across faults; error rate scales
// with the mean error rate; cascade probability scales with how many
// faults are above 50ms p50 (a crude proxy for fault density);
// memory scales with workload payload+target_rps.
func (r InProcessRunner) Run(ctx context.Context, h *Harness, trialSalt int64) (*Metrics, error) {
	if h == nil {
		return nil, errors.New("harness: nil harness")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	combined := combineSeed(h.Seed, trialSalt, h.Namespace)
	rng := rand.New(rand.NewSource(combined)) //#nosec G404 -- determinism > crypto strength

	// Aggregate fault stats (deterministic from h, no randomness).
	var sumP50 int
	var sumErr float64
	var dense int
	for _, f := range h.Faults.Faults {
		sumP50 += f.LatencyP50Ms
		sumErr += f.ErrorRate
		if f.LatencyP50Ms > 50 {
			dense++
		}
	}
	n := float64(len(h.Faults.Faults))
	if n == 0 {
		n = 1
	}
	meanP50 := float64(sumP50) / n
	meanErr := sumErr / n

	// Apply PatchedDelta as a multiplicative improvement (capped at
	// 90% of baseline to avoid degenerate zeros).
	improve := math.Max(0.0, math.Min(0.9, r.PatchedDelta))
	latencyP99 := int(meanP50 * 5 * (1.0 - improve))
	errRate := meanErr * (1.0 - improve)
	cascadeProb := float64(dense) / n * (1.0 - improve)
	if cascadeProb > 1.0 {
		cascadeProb = 1.0
	}

	// Memory scales with workload size; trialSalt jitter is small (±5%).
	jitter := 0.95 + rng.Float64()*0.10
	payloadSum := 0
	for _, e := range h.Workload.Endpoints {
		payloadSum += e.PayloadBytes
	}
	mem := int64(float64(payloadSum*h.Workload.TargetRPS) * jitter)
	if mem < 1024 {
		mem = 1024
	}

	return &Metrics{
		LatencyP99Ms:       latencyP99,
		ErrorRate:          errRate,
		CascadeProbability: cascadeProb,
		PeakMemoryBytes:    mem,
		TrialSeed:          combined,
	}, nil
}

// combineSeed mixes the harness seed, trial salt, and namespace into a
// stable per-trial seed. Uses FNV-1a so determinism survives across
// runs (math/rand-style hashes would not).
func combineSeed(seed, salt int64, ns string) int64 {
	h := fnv.New64a()
	var buf [16]byte
	binary.LittleEndian.PutUint64(buf[0:8], uint64(seed))  //#nosec G115 -- bit-for-bit reinterpretation
	binary.LittleEndian.PutUint64(buf[8:16], uint64(salt)) //#nosec G115 -- bit-for-bit reinterpretation
	_, _ = h.Write(buf[:])
	_, _ = h.Write([]byte(ns))
	return int64(h.Sum64()) //#nosec G115 -- seed is opaque, sign preservation is fine
}
