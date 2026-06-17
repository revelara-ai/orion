package sandbox

// NamespaceProvisioner + NetworkPolicyApplier implement SPEC §10.3
// sandbox isolation primitives.
//
// Per the orion-e43 scope decision, this slice ships the INTERFACE
// contracts plus in-memory test fakes. The real client-go-backed
// implementations land in orion-e46 (Live K8s harness materializer).
// Keeping the heavyweight k8s.io dependency out of this slice's
// footprint until the materializer needs it.

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// NamespaceSpec describes one per-run K8s namespace. The Conductor
// passes one of these to Provision; orion-e46's client-go-backed
// implementation translates it to the actual API objects.
type NamespaceSpec struct {
	RunID       uuid.UUID
	TenantID    uuid.UUID
	Name        string
	Labels      map[string]string
	PodSecurity string // "restricted" per SPEC §10.3
	CPULimit    string // e.g. "2"
	MemoryLimit string // e.g. "4Gi"
}

// NetworkPolicySpec describes the no-egress-except-whitelist policy
// applied to a run namespace per SPEC §10.3.
type NetworkPolicySpec struct {
	Namespace          string
	AllowedEgressCIDRs []string // kube-apiserver, Postgres svc, LLM endpoint
	AllowedEgressPorts []int32
}

// ProvisionResult is the outcome of Provision.
type ProvisionResult struct {
	Name    string
	Created bool // false when the namespace already existed
	UID     string
}

// NamespaceProvisioner is the contract the Conductor calls per run.
// Implementations MUST be idempotent on (RunID, Name): a re-call after
// crash returns the existing namespace with Created=false.
type NamespaceProvisioner interface {
	Provision(ctx context.Context, spec NamespaceSpec) (ProvisionResult, error)
	Delete(ctx context.Context, name string) error
}

// NetworkPolicyApplier applies + removes the no-egress-except policy
// for a namespace.
type NetworkPolicyApplier interface {
	Apply(ctx context.Context, spec NetworkPolicySpec) error
	Remove(ctx context.Context, namespace string) error
}

// InMemoryNamespaceProvisioner is the test fake. Records every
// Provision + Delete; concurrent Provision calls with the same Name
// dedup so the test surface mirrors the real Kubernetes API's
// AlreadyExists semantics.
type InMemoryNamespaceProvisioner struct {
	mu         sync.Mutex
	namespaces map[string]ProvisionResult
}

// NewInMemoryNamespaceProvisioner returns an empty fake.
func NewInMemoryNamespaceProvisioner() *InMemoryNamespaceProvisioner {
	return &InMemoryNamespaceProvisioner{namespaces: map[string]ProvisionResult{}}
}

// Provision is idempotent on spec.Name.
func (p *InMemoryNamespaceProvisioner) Provision(_ context.Context, spec NamespaceSpec) (ProvisionResult, error) {
	if spec.Name == "" {
		return ProvisionResult{}, errors.New("sandbox: NamespaceSpec.Name required")
	}
	if spec.RunID == uuid.Nil {
		return ProvisionResult{}, errors.New("sandbox: NamespaceSpec.RunID required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.namespaces[spec.Name]; ok {
		// Existing namespace: return Created=false so the caller can
		// treat the retry as success.
		return existing, nil
	}
	result := ProvisionResult{
		Name:    spec.Name,
		Created: true,
		UID:     uuid.NewString(),
	}
	// Store the next-call shape (Created=false) so duplicate calls
	// report the prior provision correctly.
	p.namespaces[spec.Name] = ProvisionResult{Name: spec.Name, Created: false, UID: result.UID}
	return result, nil
}

// Delete removes the namespace from the in-memory store. Absent
// namespaces are a no-op.
func (p *InMemoryNamespaceProvisioner) Delete(_ context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.namespaces, name)
	return nil
}

// Snapshot returns a copy of the current namespace registry.
func (p *InMemoryNamespaceProvisioner) Snapshot() map[string]ProvisionResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]ProvisionResult, len(p.namespaces))
	for k, v := range p.namespaces {
		out[k] = v
	}
	return out
}

// InMemoryNetworkPolicyApplier records Apply + Remove calls per
// namespace.
type InMemoryNetworkPolicyApplier struct {
	mu       sync.Mutex
	policies map[string]NetworkPolicySpec
}

// NewInMemoryNetworkPolicyApplier returns an empty fake.
func NewInMemoryNetworkPolicyApplier() *InMemoryNetworkPolicyApplier {
	return &InMemoryNetworkPolicyApplier{policies: map[string]NetworkPolicySpec{}}
}

// Apply records the policy spec. A re-apply (same namespace) overwrites,
// matching kubectl apply semantics.
func (a *InMemoryNetworkPolicyApplier) Apply(_ context.Context, spec NetworkPolicySpec) error {
	if spec.Namespace == "" {
		return errors.New("sandbox: NetworkPolicySpec.Namespace required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.policies[spec.Namespace] = spec
	return nil
}

// Remove drops the policy.
func (a *InMemoryNetworkPolicyApplier) Remove(_ context.Context, namespace string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.policies, namespace)
	return nil
}

// Get returns the recorded policy for namespace (test helper).
func (a *InMemoryNetworkPolicyApplier) Get(namespace string) (NetworkPolicySpec, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.policies[namespace]
	return s, ok
}

// RunNamespaceName builds the canonical "orion-run-<run_id>" namespace
// name. Kept here so callers don't reinvent the convention.
func RunNamespaceName(runID uuid.UUID) string {
	return fmt.Sprintf("orion-run-%s", runID)
}
