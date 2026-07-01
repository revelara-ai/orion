// Package orchestrator implements the Conductor — the single orchestrator the
// developer converses with. This is the V2.0 skeleton (or-0d2): it accepts an
// intent and returns a confirmation. Later tasks thicken it with the
// completeness gate, dispatch, truth alignment, drift re-anchoring, the circuit
// breaker, and the deployment-bar decision (PRD: Modules — orchestrator).
//
// Manifesto: the Conductor is the opinionated agentic driver of the SDLC. It is
// deliberately a narrow control-plane object, not a god-object — proof,
// decomposition, and integration live in their own modules.
package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/revelara-ai/orion/internal/budget"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/repo"
)

// Confirmation is the Conductor's acknowledgement of a submitted intent. It
// carries the OpenDecisions the completeness gate raised — the clarifying
// questions the developer must answer before the spec is complete.
type Confirmation struct {
	Intent        string
	Accepted      bool
	Message       string
	OpenDecisions []completeness.OpenDecision
}

// Status is the situational-awareness snapshot the TUI's Conversation/Fleet
// panes render. Skeleton: just the current intent.
type Status struct {
	Intent string
}

// Decision is a developer's answer to an open decision raised by the
// completeness gate. The skeleton records it; the gate consumes it later.
type Decision struct {
	Key   string
	Value string
}

// Conductor owns intent intake. Safe for concurrent use.
type Conductor struct {
	log    *slog.Logger
	store  *contextstore.Store // optional; nil = in-memory only
	gate   *completeness.Analyzer
	budget *budget.Accountant

	mu        sync.RWMutex
	intent    string
	projectID string
}

// New returns an in-memory Conductor ready to accept an intent. It
// self-instruments via the default structured logger (3 a.m. test) and an
// always-on budget accountant (no ceiling by default).
func New() *Conductor {
	return &Conductor{log: slog.Default(), gate: completeness.NewAnalyzer("http-service"), budget: budget.New()}
}

// NewWithStore returns a Conductor that persists intake to the Context Store, so
// a submitted intent (project + draft spec) survives a restart.
func NewWithStore(store *contextstore.Store) *Conductor {
	return &Conductor{log: slog.Default(), store: store, gate: completeness.NewAnalyzer("http-service"), budget: budget.New()}
}

// Budget returns the always-on resource/cost accountant.
func (c *Conductor) Budget() *budget.Accountant { return c.budget }

// Store returns the backing Context Store (nil if store-less). Used by the
// build pipeline, which needs raw store access (artifacts, STPA, deliveries).
func (c *Conductor) Store() *contextstore.Store { return c.store }

// ProjectID returns the persisted project id for the current intent, if any.
func (c *Conductor) ProjectID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.projectID
}

// currentProjectSpec loads the current project + spec AND reconstructs the
// completeness gate from the project's PERSISTED type, so a conductor reloaded in a
// later session (Submit in session 1; Approve/Build in session 2) raises the right
// per-type decisions and decomposes for the right type — instead of reverting to
// the http-service default (or-3ba.5). Every spec-flow load path goes through here
// rather than calling the store directly.
func (c *Conductor) currentProjectSpec(ctx context.Context) (contextstore.Project, contextstore.Spec, error) {
	proj, sp, err := c.store.CurrentProjectSpec(ctx)
	if err != nil {
		return proj, sp, err
	}
	if pt := proj.ProjectType; pt != "" && pt != c.gate.ProjectType() {
		c.mu.Lock()
		c.gate = completeness.NewAnalyzer(pt)
		c.mu.Unlock()
	}
	return proj, sp, err
}

// Submit intakes a developer intent and returns a confirmation. It honors
// context cancellation (every Conductor entry point is cancellable so a hung
// run can be interrupted) and rejects an empty intent rather than silently
// guessing.
func (c *Conductor) Submit(ctx context.Context, intent string) (Confirmation, error) {
	if err := ctx.Err(); err != nil {
		return Confirmation{}, fmt.Errorf("submit cancelled: %w", err)
	}
	trimmed := strings.TrimSpace(intent)
	if trimmed == "" {
		return Confirmation{}, fmt.Errorf("intent is empty: describe what you want to build")
	}

	// or-tcs.8 (step 11 → 12): before intaking a new intent, reconcile the managed repo's base with
	// its remote — the developer may have merged the prior epic's PR, so a stale local base would
	// build the next intent off the wrong head. Fast-forward if behind; BLOCK on a genuine divergence
	// (the developer's to resolve); abstain when there's no repo yet or no remote (local-first).
	if c.store != nil {
		repoDir := filepath.Join(c.store.Dir(), "repo")
		switch st, serr := repo.SyncMain(ctx, repoDir); {
		case serr != nil:
			return Confirmation{}, fmt.Errorf("reconcile managed repo with origin: %w", serr)
		case st == repo.SyncDiverged:
			return Confirmation{}, fmt.Errorf("managed base branch has diverged from origin; merge or rebase %s before a new intent", repoDir)
		case st == repo.SyncResynced:
			c.log.InfoContext(ctx, "managed base fast-forwarded to origin before intent", "repo", repoDir)
		}
	}

	c.mu.Lock()
	c.intent = trimmed
	// Choose the project type from the intent (deterministic; default http-service)
	// so the gate raises the right functional decisions and the decomposer uses the
	// right per-type task template. A clear CLI/library/worker signal switches it;
	// otherwise the V2.0 http-service path is unchanged (or-3ba.2).
	ptype := completeness.InferProjectType(trimmed)
	c.gate = completeness.NewAnalyzer(ptype)
	c.mu.Unlock()

	// Persist the intent as a project + draft spec so it survives a restart. The
	// project + spec commit as one transaction (atomic intake).
	if c.store != nil {
		var projectID string
		err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
			pid, err := tx.Projects().Create(ctx, projectName(trimmed), trimmed, ptype)
			if err != nil {
				return err
			}
			projectID = pid
			_, err = tx.Specs().CreateDraft(ctx, pid)
			return err
		})
		if err != nil {
			return Confirmation{}, fmt.Errorf("persist intent: %w", err)
		}
		c.mu.Lock()
		c.projectID = projectID
		c.mu.Unlock()
	}

	// Run the deterministic completeness gate. Unresolved dimensions become open
	// decisions the developer must answer — we never silently guess.
	open := c.gate.Analyze(trimmed, nil)

	c.log.InfoContext(ctx, "intent submitted", "intent", trimmed, "open_decisions", len(open))

	msg := fmt.Sprintf("Got it — I'll build: %s", trimmed)
	if len(open) > 0 {
		msg = fmt.Sprintf("Before I build %q, I need %d decision(s) — see the questions below.", trimmed, len(open))
	}
	return Confirmation{
		Intent:        trimmed,
		Accepted:      true,
		Message:       msg,
		OpenDecisions: open,
	}, nil
}

// Answer records a developer's answer to an open decision. Skeleton: validated
// and accepted; the completeness gate consumes answers in a later task.
func (c *Conductor) Answer(ctx context.Context, d Decision) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("answer cancelled: %w", err)
	}
	if strings.TrimSpace(d.Key) == "" {
		return fmt.Errorf("decision key is empty")
	}
	c.log.InfoContext(ctx, "decision answered", "key", d.Key, "value", d.Value)
	return nil
}

// Status returns the current situational-awareness snapshot.
func (c *Conductor) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return Status{Intent: c.intent}
}

// projectName derives a short, human-readable project name from an intent.
func projectName(intent string) string {
	fields := strings.Fields(intent)
	if len(fields) > 6 {
		fields = fields[:6]
	}
	name := strings.Join(fields, " ")
	return strings.TrimRight(name, ".,;:")
}

// Interrupt is the developer's "change direction" / abort hook. Skeleton: a
// no-op that exists so the TUI can wire the control now; later it triggers the
// same transactional rollback path as the reversibility gate.
func (c *Conductor) Interrupt() {
	c.log.Info("conductor interrupted")
}
