// Package orionsdk embeds Orion in-process (or-ykz.4, A3): open a data dir,
// drive intent → decisions → spec → proof → delivery headlessly, and consume
// typed events — the same deterministic loop the CLI and TUI run, as a
// library. CI, other agents, and tests can assert on the verdict without
// shelling out.
//
// The surface is deliberately small and stdlib-typed. The Conductor() escape
// hatch exposes the full internal API for in-module callers; external
// consumers stay on the typed methods.
package orionsdk

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/revelara-ai/orion/internal/conductor"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// Client is an in-process Orion instance over one data dir.
type Client struct {
	store *contextstore.Store
	c     *orchestrator.Conductor
}

// Open initializes (or reopens) the Context Store at dataDir.
func Open(dataDir string) (*Client, error) {
	store, err := contextstore.Open(dataDir)
	if err != nil {
		return nil, fmt.Errorf("orionsdk: %w", err)
	}
	return &Client{store: store, c: orchestrator.NewWithStore(store)}, nil
}

// Close releases the store.
func (cl *Client) Close() error { return cl.store.Close() }

// Conductor exposes the full internal API (in-module escape hatch).
func (cl *Client) Conductor() *orchestrator.Conductor { return cl.c }

// Decision is one open question the spec gate needs answered.
type Decision struct {
	Key      string `json:"key"`
	Question string `json:"question"`
}

// Confirmation reports a submitted intent and what the completeness gate
// still needs.
type Confirmation struct {
	Intent        string     `json:"intent"`
	Accepted      bool       `json:"accepted"`
	OpenDecisions []Decision `json:"open_decisions"`
}

// Submit records the intent and runs the deterministic completeness gate.
func (cl *Client) Submit(ctx context.Context, intent string) (Confirmation, error) {
	conf, err := cl.c.Submit(ctx, intent)
	if err != nil {
		return Confirmation{}, err
	}
	return toConfirmation(conf), nil
}

func toConfirmation(conf orchestrator.Confirmation) Confirmation {
	out := Confirmation{Intent: conf.Intent, Accepted: conf.Accepted, OpenDecisions: []Decision{}}
	for _, d := range conf.OpenDecisions {
		out.OpenDecisions = append(out.OpenDecisions, Decision{Key: d.Key, Question: d.Question})
	}
	return out
}

// Answer records one decision (last write wins).
func (cl *Client) Answer(ctx context.Context, key, value string) error {
	return cl.c.RecordAnswer(ctx, key, value)
}

// AddRequirement records a behavioral requirement with structured cases
// (JSON array in the add_requirement tool's case schema). The same
// validation gates apply: a case that cannot anchor is rejected.
func (cl *Client) AddRequirement(ctx context.Context, text string, casesJSON string) error {
	var cases []spec.BehavioralCase
	if err := json.Unmarshal([]byte(casesJSON), &cases); err != nil {
		return fmt.Errorf("orionsdk: cases: %w", err)
	}
	return cl.c.AddRequirement(ctx, spec.Requirement{Source: completeness.DimFunctional, Text: text, Cases: cases})
}

// ApproveAssumptions confirms the previewed fallback assumptions.
func (cl *Client) ApproveAssumptions(ctx context.Context) ([]string, error) {
	return cl.c.ApproveAssumptions(ctx)
}

// Ratify freezes the spec (hash-anchored); the build proves against it.
func (cl *Client) Ratify(ctx context.Context) (specHash string, err error) {
	es, err := cl.c.ApproveSpec(ctx)
	if err != nil {
		return "", err
	}
	return es.Hash, nil
}

// Event is one typed loop event (the machine-mode stream, in-process).
type Event struct {
	Seq    int    `json:"seq"`
	Phase  string `json:"phase"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Outcome is the end state of a build run.
type Outcome struct {
	TaskID   string `json:"task_id"`
	Verdict  string `json:"verdict"`
	Closed   bool   `json:"closed"`
	Tier     string `json:"tier"`
	Delivery string `json:"delivery"`
}

// BuildService runs the full decompose → generate → prove → deliver loop on
// the ratified spec. A nil onEvent discards events. The default generator is
// Orion's deterministic fixture path; in-module callers needing a real
// generator use Conductor() with the conductor package directly.
func (cl *Client) BuildService(ctx context.Context, onEvent func(Event)) (Outcome, error) {
	seq := 0
	var sink conductor.PhaseSink
	if onEvent != nil {
		sink = func(e conductor.PhaseEvent) {
			seq++
			onEvent(Event{Seq: seq, Phase: e.Phase, Status: string(e.Status), Detail: e.Detail})
		}
	}
	res, err := conductor.BuildAndProve(ctx, cl.store, nil, nil, sink, "")
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{TaskID: res.TaskID, Verdict: string(res.Verdict), Closed: res.Closed, Tier: string(res.Tier), Delivery: res.Delivery}, nil
}
