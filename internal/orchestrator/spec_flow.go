package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// SpecView is the Spec-review pane / `orion spec show` projection.
type SpecView struct {
	Intent           string                      `json:"intent"`
	Status           string                      `json:"status"`
	Hash             string                      `json:"hash"`
	OpenDecisions    []completeness.OpenDecision `json:"open_decisions"`
	ResponseContract json.RawMessage             `json:"response_contract,omitempty"`
}

// errNoStore guards the spec-flow methods, which require persistence.
var errNoStore = fmt.Errorf("no context store: spec flow requires a persistent store")

// RecordAnswer persists a developer's answer to a required decision on the
// current draft spec.
func (c *Conductor) RecordAnswer(ctx context.Context, key, value string) error {
	if c.store == nil {
		return errNoStore
	}
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("decision key is empty")
	}
	proj, sp, err := c.store.CurrentProjectSpec(ctx)
	if err != nil {
		return fmt.Errorf("no current spec to answer: %w", err)
	}
	if err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		_, e := tx.Decisions().Create(ctx, proj.ID, sp.ID, key, value, "precise", false)
		return e
	}); err != nil {
		return err
	}
	c.log.InfoContext(ctx, "decision answered", "key", key)
	return nil
}

// loadAnswers returns the answer + value-kind maps for a spec.
func (c *Conductor) loadAnswers(ctx context.Context, specID string) (answers, kinds map[string]string, err error) {
	ds, err := c.store.DecisionsForSpec(ctx, specID)
	if err != nil {
		return nil, nil, err
	}
	answers, kinds = map[string]string{}, map[string]string{}
	for _, d := range ds {
		answers[d.Key] = d.Value
		kinds[d.Key] = d.ValueKind
	}
	return answers, kinds, nil
}

// ApproveSpec ratifies the current spec: every blocking (no-fallback) decision
// must be answered; remaining fallback-eligible decisions are resolved to their
// presets; then the spec is compiled (ResponseContract + hash), its typed
// dimensions persisted, and it is marked accepted + anchored.
func (c *Conductor) ApproveSpec(ctx context.Context) (spec.ExecutableSpec, error) {
	if c.store == nil {
		return spec.ExecutableSpec{}, errNoStore
	}
	proj, sp, err := c.store.CurrentProjectSpec(ctx)
	if err != nil {
		return spec.ExecutableSpec{}, fmt.Errorf("no current spec to approve: %w", err)
	}
	answers, kinds, err := c.loadAnswers(ctx, sp.ID)
	if err != nil {
		return spec.ExecutableSpec{}, err
	}

	open := c.gate.Analyze(proj.Intent, answers)
	var blocking []string
	type fb struct{ key, value string }
	var fallbacks []fb
	for _, od := range open {
		if od.Fallback == "" {
			blocking = append(blocking, od.Key)
			continue
		}
		fallbacks = append(fallbacks, fb{od.Key, fallbackValue(od)})
	}
	if len(blocking) > 0 {
		return spec.ExecutableSpec{}, fmt.Errorf("cannot approve: unanswered decision(s) with no fallback: %s", strings.Join(blocking, ", "))
	}
	for _, f := range fallbacks {
		answers[f.key] = f.value
		kinds[f.key] = "fallback_preset"
	}

	es, err := spec.Compile(proj.Intent, answers, kinds, c.gate.Checklist())
	if err != nil {
		return spec.ExecutableSpec{}, err
	}
	rcJSON, err := json.Marshal(es.ResponseContract)
	if err != nil {
		return spec.ExecutableSpec{}, fmt.Errorf("marshal response contract: %w", err)
	}

	if err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		for _, f := range fallbacks {
			if _, e := tx.Decisions().Create(ctx, proj.ID, sp.ID, f.key, f.value, "fallback_preset", false); e != nil {
				return e
			}
		}
		for _, dim := range es.Dimensions {
			vs, e := json.Marshal(dim.Values)
			if e != nil {
				return e
			}
			if e := tx.SpecDimensions().Upsert(ctx, sp.ID, string(dim.Name), string(vs), dim.ValueKind, false); e != nil {
				return e
			}
		}
		return tx.Specs().SetAccepted(ctx, sp.ID, es.Hash, string(rcJSON))
	}); err != nil {
		return spec.ExecutableSpec{}, err
	}
	c.log.InfoContext(ctx, "spec accepted", "spec_id", sp.ID, "hash", es.Hash)
	return es, nil
}

// SpecView returns the current spec projection (open decisions recomputed from
// stored answers — the single source of "what's open").
func (c *Conductor) SpecView(ctx context.Context) (SpecView, error) {
	if c.store == nil {
		return SpecView{}, errNoStore
	}
	proj, sp, err := c.store.CurrentProjectSpec(ctx)
	if err != nil {
		return SpecView{}, fmt.Errorf("no current spec: %w", err)
	}
	answers, _, err := c.loadAnswers(ctx, sp.ID)
	if err != nil {
		return SpecView{}, err
	}
	open := c.gate.Analyze(proj.Intent, answers)
	v := SpecView{
		Intent:        proj.Intent,
		Status:        sp.Status,
		Hash:          sp.Hash,
		OpenDecisions: open,
	}
	if sp.ResponseContract != "" && sp.ResponseContract != "{}" {
		v.ResponseContract = json.RawMessage(sp.ResponseContract)
	}
	return v, nil
}

// RecallSpec re-derives the accepted spec from the Context Store and verifies its
// anchor: it recompiles from the stored decisions and checks the hash matches the
// persisted one. A mismatch means the spec was tampered with (Trust invariant 7:
// proof reads the spec from the anchor-verified store).
func (c *Conductor) RecallSpec(ctx context.Context) (spec.ExecutableSpec, error) {
	if c.store == nil {
		return spec.ExecutableSpec{}, errNoStore
	}
	proj, sp, err := c.store.CurrentProjectSpec(ctx)
	if err != nil {
		return spec.ExecutableSpec{}, err
	}
	if sp.Status != "accepted" {
		return spec.ExecutableSpec{}, fmt.Errorf("spec is not accepted (status=%s)", sp.Status)
	}
	answers, kinds, err := c.loadAnswers(ctx, sp.ID)
	if err != nil {
		return spec.ExecutableSpec{}, err
	}
	es, err := spec.Compile(proj.Intent, answers, kinds, c.gate.Checklist())
	if err != nil {
		return spec.ExecutableSpec{}, fmt.Errorf("recompile on recall: %w", err)
	}
	if es.Hash != sp.Hash {
		return spec.ExecutableSpec{}, fmt.Errorf("spec anchor mismatch: stored=%s recomputed=%s (tampered?)", sp.Hash, es.Hash)
	}
	return es, nil
}

// PlanTask is one task in the rendered plan.
type PlanTask struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	ProofObligation string   `json:"proof_obligation"`
	FileScope       string   `json:"file_scope"`
	DependsOn       []string `json:"depends_on"`
}

// PlanView is the Plan pane / `orion plan show` projection.
type PlanView struct {
	EpicTitle string     `json:"epic_title"`
	Tasks     []PlanTask `json:"tasks"`
}

// PlanView returns the Epic/Task plan for the current accepted spec, decomposing
// it on demand (and persisting) the first time. The decomposition is gated: every
// spec requirement must map to >=1 ProofObligation before the plan is persisted.
func (c *Conductor) PlanView(ctx context.Context) (PlanView, error) {
	if c.store == nil {
		return PlanView{}, errNoStore
	}
	proj, sp, err := c.store.CurrentProjectSpec(ctx)
	if err != nil {
		return PlanView{}, fmt.Errorf("no current spec: %w", err)
	}
	if sp.Status != "accepted" {
		return PlanView{}, fmt.Errorf("spec is not accepted (status=%s); approve it before planning", sp.Status)
	}

	if _, err := c.ensurePlan(ctx, proj, sp); err != nil {
		return PlanView{}, err
	}
	return c.readPlan(ctx, proj)
}

// ensurePlan decomposes + persists the plan if no epic exists yet (idempotent).
//
// NOTE: decomposition (RecallSpec, which opens its own read transactions) is done
// BEFORE the write transaction — the Context Store caps connections at one, so
// opening a nested transaction inside WithTx would deadlock.
func (c *Conductor) ensurePlan(ctx context.Context, proj contextstore.Project, sp contextstore.Spec) (string, error) {
	// Fast path: a plan already exists.
	if existing, err := c.currentEpicID(ctx, proj.ID); err == nil {
		return existing, nil
	} else if !errors.Is(err, contextstore.ErrNotFound) {
		return "", err
	}

	// Decompose the anchor-verified spec (uses its own read transactions).
	es, err := c.RecallSpec(ctx)
	if err != nil {
		return "", err
	}
	epic := decomposer.Decompose(es)
	if err := decomposer.CoverageGate(es, epic); err != nil {
		return "", err
	}

	var epicID string
	err = c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		// Re-check inside the write tx to avoid a double-decompose race.
		if existing, e := tx.Epics().LatestForProject(ctx, proj.ID); e == nil {
			epicID = existing.ID
			return nil
		} else if !errors.Is(e, contextstore.ErrNotFound) {
			return e
		}
		eid, err := tx.Epics().Create(ctx, proj.ID, sp.ID, epic.Title)
		if err != nil {
			return err
		}
		epicID = eid
		keyToID := map[string]string{}
		for _, task := range epic.Tasks {
			tid, err := tx.Tasks().Create(ctx, eid, task.Title, task.FileScope)
			if err != nil {
				return err
			}
			keyToID[task.Key] = tid
			if _, err := tx.ProofObligations().Create(ctx, tid, task.ProofObligation); err != nil {
				return err
			}
		}
		for _, task := range epic.Tasks {
			for _, dep := range task.DependsOn {
				if err := tx.Tasks().AddDep(ctx, keyToID[task.Key], keyToID[dep]); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return epicID, err
}

// currentEpicID returns the latest epic id for a project (read-only).
func (c *Conductor) currentEpicID(ctx context.Context, projectID string) (string, error) {
	var id string
	err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		e, err := tx.Epics().LatestForProject(ctx, projectID)
		if err != nil {
			return err
		}
		id = e.ID
		return nil
	})
	return id, err
}

func (c *Conductor) readPlan(ctx context.Context, proj contextstore.Project) (PlanView, error) {
	var pv PlanView
	err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		epic, err := tx.Epics().LatestForProject(ctx, proj.ID)
		if err != nil {
			return err
		}
		pv.EpicTitle = epic.Title
		tasks, err := tx.Tasks().ListByEpic(ctx, epic.ID)
		if err != nil {
			return err
		}
		for _, t := range tasks {
			obligations, err := tx.ProofObligations().ListForTask(ctx, t.ID)
			if err != nil {
				return err
			}
			deps, err := tx.Tasks().DepsOf(ctx, t.ID)
			if err != nil {
				return err
			}
			ob := ""
			if len(obligations) > 0 {
				ob = obligations[0]
			}
			pv.Tasks = append(pv.Tasks, PlanTask{
				ID:              t.ID,
				Title:           t.Title,
				ProofObligation: ob,
				FileScope:       t.FileScope,
				DependsOn:       deps,
			})
		}
		return nil
	})
	return pv, err
}

// fallbackValue chooses the concrete value a fallback-eligible decision defaults
// to on approval. Scale uses the 'medium' preset (expands to a concrete
// threshold); other dimensions take their documented fallback text.
func fallbackValue(od completeness.OpenDecision) string {
	if od.Dimension == completeness.DimScale {
		return "medium"
	}
	return od.Fallback
}
