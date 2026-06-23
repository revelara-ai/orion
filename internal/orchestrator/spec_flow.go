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

// loadRequirements decodes the persisted requirements JSON (empty → nil).
func loadRequirements(jsonStr string) []spec.Requirement {
	jsonStr = strings.TrimSpace(jsonStr)
	if jsonStr == "" || jsonStr == "[]" {
		return nil
	}
	var reqs []spec.Requirement
	if json.Unmarshal([]byte(jsonStr), &reqs) != nil {
		return nil
	}
	return reqs
}

// AddRequirement records a structured behavioral requirement on the current draft
// spec. The requirement must lower to executable cases (validated here — fail-loud
// at intake, the or-y9d invariant at the elicitation seam). Re-adding the same
// requirement (by content-addressed id) is idempotent.
func (c *Conductor) AddRequirement(ctx context.Context, req spec.Requirement) error {
	if c.store == nil {
		return errNoStore
	}
	if err := spec.ValidateRequirement(req); err != nil {
		return err
	}
	req.SetIDs()
	_, sp, err := c.currentProjectSpec(ctx)
	if err != nil {
		return fmt.Errorf("no current spec to add a requirement: %w", err)
	}
	reqs := loadRequirements(sp.Requirements)
	for _, r := range reqs {
		if r.ID == req.ID {
			return nil // already recorded
		}
	}
	reqs = append(reqs, req)
	b, err := json.Marshal(reqs)
	if err != nil {
		return err
	}
	if err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.Specs().SetRequirements(ctx, sp.ID, string(b))
	}); err != nil {
		return err
	}
	c.log.InfoContext(ctx, "requirement added", "id", req.ID, "cases", len(req.Cases))
	return nil
}

// Requirements returns the structured behavioral requirements recorded on the
// current draft spec (for review before ratifying).
func (c *Conductor) Requirements(ctx context.Context) ([]spec.Requirement, error) {
	if c.store == nil {
		return nil, errNoStore
	}
	_, sp, err := c.currentProjectSpec(ctx)
	if err != nil {
		return nil, fmt.Errorf("no current spec: %w", err)
	}
	return loadRequirements(sp.Requirements), nil
}

// RecordAnswer persists a developer's answer to a required decision on the
// current draft spec.
func (c *Conductor) RecordAnswer(ctx context.Context, key, value string) error {
	if c.store == nil {
		return errNoStore
	}
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("decision key is empty")
	}
	// A scalar decision must not smuggle multi-clause behavior — that belongs in a
	// Requirement with verifiable cases (or-y9d: a behavioral paragraph in the
	// timezone slot was never proven). Reject it loudly with a redirect.
	if spec.IsConditionalValue(value) {
		return fmt.Errorf("%q reads as conditional behavior, not a single value — capture it with add_requirement (explicit request→expected cases) so it can be proven", key)
	}
	proj, sp, err := c.currentProjectSpec(ctx)
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

// DecisionKeys returns the set of valid decision keys (the completeness
// checklist) — used to validate a developer's spec edit before recording it.
func (c *Conductor) DecisionKeys() map[string]bool {
	keys := map[string]bool{}
	for _, rd := range c.gate.Checklist() {
		keys[rd.Key] = true
	}
	return keys
}

// fallbackKV is a fallback-eligible decision resolved to its preset value.
type fallbackKV struct{ key, value string }

// assembleSpec resolves the current draft into an ExecutableSpec WITHOUT
// accepting it: blocking (no-fallback) decisions must be answered; remaining
// fallback-eligible decisions resolve to their presets; the spec is compiled
// (ResponseContract + hash). Used by both PreviewSpec (review) and ApproveSpec
// (ratify) so what the developer reviews is exactly what gets accepted.
func (c *Conductor) assembleSpec(ctx context.Context) (contextstore.Project, contextstore.Spec, spec.ExecutableSpec, []fallbackKV, error) {
	var proj contextstore.Project
	var sp contextstore.Spec
	if c.store == nil {
		return proj, sp, spec.ExecutableSpec{}, nil, errNoStore
	}
	proj, sp, err := c.currentProjectSpec(ctx)
	if err != nil {
		return proj, sp, spec.ExecutableSpec{}, nil, fmt.Errorf("no current spec: %w", err)
	}
	answers, kinds, err := c.loadAnswers(ctx, sp.ID)
	if err != nil {
		return proj, sp, spec.ExecutableSpec{}, nil, err
	}
	// Intent-stated functional decisions are USABLE, not dropped: apply the values the
	// intent explicitly states (deterministically re-derived from the intent) so an
	// intent that names a port/format/route compiles instead of erroring "unresolved"
	// (or-jh7). An explicit stored answer always wins.
	for k, v := range c.gate.IntentValues(proj.Intent) {
		if strings.TrimSpace(answers[k]) == "" {
			answers[k] = v
			kinds[k] = "precise"
		}
	}

	open := c.gate.Analyze(proj.Intent, answers)
	var blocking []string
	var fallbacks []fallbackKV
	for _, od := range open {
		if od.Fallback == "" {
			blocking = append(blocking, od.Key)
			continue
		}
		fallbacks = append(fallbacks, fallbackKV{od.Key, fallbackValue(od)})
	}
	if len(blocking) > 0 {
		return proj, sp, spec.ExecutableSpec{}, nil, fmt.Errorf("unanswered decision(s) with no fallback: %s", strings.Join(blocking, ", "))
	}
	for _, f := range fallbacks {
		answers[f.key] = f.value
		kinds[f.key] = "fallback_preset"
	}

	es, err := spec.Compile(proj.Intent, answers, kinds, c.gate.Checklist(), loadRequirements(sp.Requirements))
	if err != nil {
		return proj, sp, spec.ExecutableSpec{}, nil, err
	}
	return proj, sp, es, fallbacks, nil
}

// PreviewSpec returns the assembled ExecutableSpec for developer review WITHOUT
// accepting it (fallback-eligible dimensions resolved to presets). Nothing is
// persisted — the spec is shown before it is ratified.
func (c *Conductor) PreviewSpec(ctx context.Context) (spec.ExecutableSpec, error) {
	_, _, es, _, err := c.assembleSpec(ctx)
	return es, err
}

// ApproveSpec ratifies the current spec: it assembles exactly what PreviewSpec
// showed, persists the fallback decisions + typed dimensions, and marks the spec
// accepted + anchored.
func (c *Conductor) ApproveSpec(ctx context.Context) (spec.ExecutableSpec, error) {
	proj, sp, es, fallbacks, err := c.assembleSpec(ctx)
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
	proj, sp, err := c.currentProjectSpec(ctx)
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
	proj, sp, err := c.currentProjectSpec(ctx)
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
	es, err := spec.Compile(proj.Intent, answers, kinds, c.gate.Checklist(), loadRequirements(sp.Requirements))
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
	proj, sp, err := c.currentProjectSpec(ctx)
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
	epic := decomposer.Decompose(es, c.gate.ProjectType())
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
