// Package agentruntime is the specialist-agent registry and lifecycle for the
// GENERATION domain. It spawns single-task agents (generators, instrumentors,
// resolvers, rvl:* detectors) — each bounded, cancellable, and deadline-capped —
// and dispatches work to them over the in-process a2a bus, recording an untrusted
// EvidenceClaim as a task attempt.
//
// Trust-domain note (manifesto: no agent grades its own homework): everything
// here is the untrusted generation domain. Dispatch records the agent's
// EvidenceClaim as a *claim* on the attempt; it never writes a proof/verdict.
// The behavioral test author lives in the proof domain, NOT here.
package agentruntime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/revelara-ai/orion/internal/a2a"
	"github.com/revelara-ai/orion/internal/contextstore"
)

// Agent is a generation-domain specialist that performs one task.
type Agent interface {
	Role() string
	Run(ctx context.Context, req a2a.Request) (a2a.EvidenceClaim, error)
}

// Factory builds a fresh Agent instance for a spawn.
type Factory func() Agent

// AgentStatus is the lifecycle state surfaced to the Fleet pane.
type AgentStatus string

const (
	StatusRunning   AgentStatus = "running"
	StatusDone      AgentStatus = "done"
	StatusFailed    AgentStatus = "failed"
	StatusCancelled AgentStatus = "cancelled"
)

// Handle is a spawned agent's lifecycle handle. Cancel aborts the in-flight run.
type Handle struct {
	Role   string
	TaskID string

	mu     sync.Mutex
	status AgentStatus
	cancel context.CancelFunc
	agent  Agent
}

// Status returns the current lifecycle state.
func (h *Handle) Status() AgentStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status
}

// Cancel aborts the agent's in-flight context and marks it cancelled.
func (h *Handle) Cancel() {
	h.mu.Lock()
	cancel := h.cancel
	h.status = StatusCancelled
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (h *Handle) setStatus(s AgentStatus) {
	h.mu.Lock()
	if h.status != StatusCancelled { // a cancel wins over a late status
		h.status = s
	}
	h.mu.Unlock()
}

func (h *Handle) bindCancel(c context.CancelFunc) {
	h.mu.Lock()
	h.cancel = c
	h.mu.Unlock()
}

// FleetEntry is a Fleet-pane row: which agent is doing what, with what status.
type FleetEntry struct {
	Role   string
	TaskID string
	Status AgentStatus
}

// Registry holds agent factories and tracks the live fleet.
type Registry struct {
	mu        sync.Mutex
	factories map[string]Factory
	handles   []*Handle
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{factories: map[string]Factory{}} }

// Register binds a role to a factory.
func (r *Registry) Register(role string, f Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[role] = f
}

// Spawn instantiates a fresh agent for a role+task and tracks its handle.
func (r *Registry) Spawn(role, taskID string) (*Handle, error) {
	r.mu.Lock()
	f, ok := r.factories[role]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("agentruntime: no agent registered for role %q", role)
	}
	h := &Handle{Role: role, TaskID: taskID, status: StatusRunning, agent: f()}
	r.mu.Lock()
	r.handles = append(r.handles, h)
	r.mu.Unlock()
	return h, nil
}

// Fleet returns a snapshot of all spawned agents (Fleet pane data).
func (r *Registry) Fleet() []FleetEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]FleetEntry, 0, len(r.handles))
	for _, h := range r.handles {
		out = append(out, FleetEntry{Role: h.Role, TaskID: h.TaskID, Status: h.Status()})
	}
	return out
}

// Dispatcher runs a task on a spawned specialist over the a2a bus, enforcing a
// wall-clock deadline, and records the untrusted EvidenceClaim as an attempt. A
// bounded dispatch semaphore caps concurrency with backpressure.
type Dispatcher struct {
	reg     *Registry
	store   *contextstore.Store
	timeout time.Duration
	sem     chan struct{} // nil = unbounded

	mu       sync.Mutex
	inflight map[string]*dispatchCall // per-task dispatch lock (no double-assign)
}

// dispatchCall is a single in-flight assignment for a task; concurrent dispatches
// of the same task wait on it and share its result (singleflight).
type dispatchCall struct {
	done  chan struct{}
	claim a2a.EvidenceClaim
	err   error
}

// Option configures a Dispatcher.
type Option func(*Dispatcher)

// WithMaxConcurrency bounds the number of concurrently-dispatched agents.
// Further dispatches block (backpressure) until a slot frees.
func WithMaxConcurrency(n int) Option {
	return func(d *Dispatcher) {
		if n > 0 {
			d.sem = make(chan struct{}, n)
		}
	}
}

// NewDispatcher wires a registry + Context Store with a per-step deadline.
func NewDispatcher(reg *Registry, store *contextstore.Store, timeout time.Duration, opts ...Option) *Dispatcher {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	d := &Dispatcher{reg: reg, store: store, timeout: timeout, inflight: map[string]*dispatchCall{}}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Dispatch spawns the role's agent, runs it (deadline + cancel) over the a2a bus,
// and records a task_attempt carrying the untrusted EvidenceClaim. It does NOT
// produce a verdict — proof is computed later, independently, by the proof
// domain.
func (d *Dispatcher) Dispatch(ctx context.Context, req a2a.Request, idempotencyKey string) (claim a2a.EvidenceClaim, err error) {
	taskID := req.Obligation.TaskID

	// Per-task dispatch lock + idempotency: if a dispatch for this task is already
	// in flight, share its single assignment rather than spawning a second agent
	// (no double-assign across concurrent dispatch; complements path leases).
	d.mu.Lock()
	if c, ok := d.inflight[taskID]; ok {
		d.mu.Unlock()
		select {
		case <-c.done:
			return c.claim, c.err
		case <-ctx.Done():
			return a2a.EvidenceClaim{}, ctx.Err()
		}
	}
	call := &dispatchCall{done: make(chan struct{})}
	d.inflight[taskID] = call
	d.mu.Unlock()
	defer func() {
		call.claim, call.err = claim, err
		d.mu.Lock()
		delete(d.inflight, taskID)
		d.mu.Unlock()
		close(call.done)
	}()

	// Bounded concurrency: acquire a dispatch slot (backpressure when full),
	// honoring cancellation while waiting.
	if d.sem != nil {
		select {
		case d.sem <- struct{}{}:
			defer func() { <-d.sem }()
		case <-ctx.Done():
			return a2a.EvidenceClaim{}, ctx.Err()
		}
	}

	h, err := d.reg.Spawn(req.Role, taskID)
	if err != nil {
		return a2a.EvidenceClaim{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	h.bindCancel(cancel)

	// Route through the a2a bus: the agent's Run is wrapped as a transient handler
	// so the protocol path (and id-stamping) is exercised on every dispatch.
	bus := a2a.NewBus()
	bus.Register(req.Role, a2a.HandlerFunc(func(r a2a.Request) (a2a.EvidenceClaim, error) {
		return h.agent.Run(runCtx, r)
	}))

	claim, err = bus.Send(req)
	if err != nil {
		h.setStatus(StatusFailed)
		return a2a.EvidenceClaim{}, fmt.Errorf("dispatch %s: %w", req.Role, err)
	}
	h.setStatus(StatusDone)

	// Persist the untrusted claim on the attempt (idempotency key dedups retries).
	claimJSON, err := a2a.MarshalClaim(claim)
	if err != nil {
		return a2a.EvidenceClaim{}, fmt.Errorf("marshal claim: %w", err)
	}
	if err := d.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		_, e := tx.Attempts().CreateWithClaim(ctx, taskID, idempotencyKey, string(claimJSON))
		return e
	}); err != nil {
		return a2a.EvidenceClaim{}, fmt.Errorf("record attempt: %w", err)
	}
	return claim, nil
}
