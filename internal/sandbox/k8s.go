package sandbox

// K8sPodCreator wraps the kubernetes-side of the orion-worker spawn
// path (SPEC §11.1). Implementations MUST be idempotent on
// workspace_key: the Conductor records a SpawnIntent with the
// workspace_key as part of the claim transaction, then calls Create.
// If the Conductor crashes after recording the intent but before the
// pod actually lands, a subsequent retry MUST observe the existing
// pod as success rather than re-creating it.
//
// This package ships:
//
//   - The K8sPodCreator interface (the contract).
//   - WorkspaceKey, the deterministic key derivation from
//     (org_id, claim_id, run_id).
//   - InMemoryPodCreator, a test fake that exercises the idempotency
//     contract without any cluster.
//
// The real client-go implementation lives in orion-e46 (Live K8s
// harness materializer). Deferring it here keeps the heavyweight
// k8s.io dependency out of this slice's footprint.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"

	"github.com/google/uuid"
)

// ErrPodAlreadyExists is the sentinel an implementation MUST return
// when a Create call observes that the workspace_key already maps to
// an existing pod. Callers translate this to a success path: the
// prior leader's pod is the canonical one.
var ErrPodAlreadyExists = errors.New("sandbox: pod for workspace_key already exists")

// PodCreateIntent is the input to K8sPodCreator.Create. Fields
// mirror the worker_spawn_intents row plus the orion-worker image.
type PodCreateIntent struct {
	Namespace      string
	PodName        string
	WorkspaceKey   string
	ContainerImage string
	// Env carries the orion-worker startup vars (RUN_ID, CLAIM_ID,
	// WORKSPACE_KEY, FENCING_TOKEN). Implementations pass through.
	Env map[string]string
}

// PodCreateResult is the output of Create. Created is true when a new
// pod was provisioned; Created is false (and err is nil) when the
// workspace_key already had a pod and the implementation handled
// AlreadyExists as success.
type PodCreateResult struct {
	Created      bool
	PodName      string
	Namespace    string
	WorkspaceKey string
}

// K8sPodCreator is the contract that orion-e48 (Conductor) calls to
// materialize a worker pod once the claim+spawn-intent transaction
// has committed. Idempotency on workspace_key is non-negotiable.
type K8sPodCreator interface {
	Create(ctx context.Context, intent PodCreateIntent) (PodCreateResult, error)
}

// WorkspaceKey derives the deterministic SPEC §10.4-sanitized
// workspace key from (orgID, claimID, runID). Output is a 16-hex-char
// sha256 prefix. Determinism is the load-bearing property: two
// Conductor replicas computing the same key MUST produce identical
// output so the UNIQUE constraint on worker_sessions.workspace_key /
// worker_spawn_intents.workspace_key dedups correctly.
func WorkspaceKey(orgID, claimID, runID uuid.UUID) string {
	h := sha256.New()
	h.Write(orgID[:])
	h.Write(claimID[:])
	h.Write(runID[:])
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}

// InMemoryPodCreator is a fake K8sPodCreator suitable for unit tests
// of upstream code that calls Create. The real client-go-backed
// implementation lives in orion-e46.
//
// Concurrent calls with the same workspace_key are deduplicated under
// the package's mutex: exactly one Create returns Created=true; the
// rest return Created=false (no error). Calls with different keys
// proceed independently.
type InMemoryPodCreator struct {
	mu   sync.Mutex
	pods map[string]PodCreateResult
}

// NewInMemoryPodCreator returns an empty in-memory fake.
func NewInMemoryPodCreator() *InMemoryPodCreator {
	return &InMemoryPodCreator{pods: map[string]PodCreateResult{}}
}

// Create implements K8sPodCreator using an in-memory map keyed by
// workspace_key. Treats duplicate workspace_key as success per the
// idempotency contract.
func (c *InMemoryPodCreator) Create(_ context.Context, intent PodCreateIntent) (PodCreateResult, error) {
	if intent.WorkspaceKey == "" {
		return PodCreateResult{}, errors.New("sandbox: WorkspaceKey is required")
	}
	if intent.Namespace == "" || intent.PodName == "" {
		return PodCreateResult{}, errors.New("sandbox: Namespace and PodName are required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.pods[intent.WorkspaceKey]; ok {
		// Idempotent: return the existing pod as a non-error success.
		// Callers treat Created=false as "the prior leader's pod is
		// canonical; reuse it."
		return existing, nil
	}
	result := PodCreateResult{
		Created:      true,
		PodName:      intent.PodName,
		Namespace:    intent.Namespace,
		WorkspaceKey: intent.WorkspaceKey,
	}
	c.pods[intent.WorkspaceKey] = PodCreateResult{
		Created:      false, // future calls observe an existing pod
		PodName:      intent.PodName,
		Namespace:    intent.Namespace,
		WorkspaceKey: intent.WorkspaceKey,
	}
	return result, nil
}

// Pods returns a snapshot of the in-memory state for tests.
func (c *InMemoryPodCreator) Pods() map[string]PodCreateResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]PodCreateResult, len(c.pods))
	for k, v := range c.pods {
		out[k] = v
	}
	return out
}
