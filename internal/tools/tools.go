// Package tools is the agent harness's tool registry (native-harness Phase 2).
// Tools are injected explicitly (NOT registered via init() globals — a Go
// anti-pattern) and carry safety metadata checked before dispatch. Registration
// is separate from EXPOSURE: each agent constructs a Registry holding only the
// tools it may use (the grilling Orion agent gets a different surface than a
// delivery agent).
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/revelara-ai/orion/pkg/llm"
)

// Safety classifies a tool so the harness/gates can decide how to dispatch it
// (read-only tools run freely; destructive ones route through approval/proof).
type Safety struct {
	ReadOnly     bool
	Destructive  bool
	ParallelSafe bool
	// RequiresApproval marks a tool that acts in the developer's ENVIRONMENT and should
	// be approved per-call by the human (bash, write_file, edit_file). It is distinct
	// from Destructive: the spec/change-pipeline tools are Destructive (they mutate
	// Orion's own state) but internal, and must NOT trigger a per-call user prompt.
	RequiresApproval bool
}

// Tool is an executable capability exposed to a model.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Safety      Safety
	// Run executes the tool with the model-provided JSON input and returns the
	// tool_result content (a string the model reads back).
	Run func(ctx context.Context, input json.RawMessage) (string, error)
}

// Registry is an explicit, per-agent set of exposed tools.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	order []string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{tools: map[string]Tool{}} }

// Register adds (or replaces) a tool.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[t.Name]; !ok {
		r.order = append(r.order, t.Name)
	}
	r.tools[t.Name] = t
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Specs returns the provider-facing tool declarations (name/description/schema),
// in registration order.
func (r *Registry) Specs() []llm.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]llm.Tool, 0, len(r.order))
	for _, n := range r.order {
		t := r.tools[n]
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out = append(out, llm.Tool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	return out
}

// Names returns the registered tool names, sorted.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ns := make([]string, len(r.order))
	copy(ns, r.order)
	sort.Strings(ns)
	return ns
}

// Dispatch runs a tool by name, returning the result content + whether it errored.
// An unknown tool is a (handled) error, not a panic — the model gets a tool_result
// telling it so.
func (r *Registry) Dispatch(ctx context.Context, name string, input json.RawMessage) (string, bool) {
	t, ok := r.Get(name)
	if !ok {
		return fmt.Sprintf("unknown tool %q", name), true
	}
	out, err := t.Run(ctx, input)
	if err != nil {
		return err.Error(), true
	}
	return out, false
}
