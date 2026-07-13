package agentruntime

import (
	"context"

	"github.com/revelara-ai/orion/internal/a2a"
)

// RoleGoGenerator is the role name for the Go service generator.
const RoleGoGenerator = "go-generator"

// StubGenerator is a placeholder generation-domain agent: it returns an
// EvidenceClaim asserting it implemented the task, without producing real
// artifacts. Real sandboxed Go generation replaces it in a later task (the
// sandbox + worktree + real generation work item). It exists so the dispatch
// path is end-to-end now.
type StubGenerator struct{}

// Role identifies this agent.
func (StubGenerator) Role() string { return RoleGoGenerator }

// Run honors context cancellation/deadline and returns an untrusted claim.
func (StubGenerator) Run(ctx context.Context, _ a2a.Request) (a2a.EvidenceClaim, error) {
	if err := ctx.Err(); err != nil {
		return a2a.EvidenceClaim{}, err
	}
	return a2a.EvidenceClaim{
		AssertionStatus: "implemented",
		ArtifactRefs: []a2a.ArtifactRef{
			{Type: "code", StoragePath: "(stub)", ContentHash: ""},
		},
	}, nil
}

// DefaultRegistry returns a registry pre-loaded with the built-in generation
// agents. The Conductor's dispatch flow uses this once decomposition produces
// tasks to dispatch.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(RoleGoGenerator, func() Agent { return StubGenerator{} })
	return r
}
