package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/pkg/llmclient"
)

// recordShadowPlan runs the injected semantic ModuleProposer in SHADOW and
// persists how its plan compares to the oracle decomposer's (or-809). Entirely
// best-effort: it never returns an error and never mutates the live plan — the
// oracle epic drives the build. The measured window over these records is the
// eventual cutover criterion (coverage superset AND cluster-count non-regression).
func (c *Conductor) recordShadowPlan(ctx context.Context, projectID string, es spec.ExecutableSpec, oracle decomposer.Epic) {
	// I2: the shadow path must NEVER fail the plan — a panic in the (adversarial,
	// LLM-backed) proposer is contained here, not propagated.
	defer func() {
		if r := recover(); r != nil {
			c.log.WarnContext(ctx, "module proposer shadow: recovered from panic", "panic", r)
		}
	}()
	// Shadow runs are BACKGROUND traffic (or-mvr.3): they draw from the same
	// in-flight cap as interactive work and are shed first under pressure —
	// first-party background load must never starve the live plan path.
	ctx = llmclient.WithTrafficClass(ctx, llmclient.ClassBackground)
	floor := decomposer.DefaultFloor()
	pe, err := decomposer.Propose(ctx, es, c.gate.ProjectType(), floor, c.proposer)
	if err != nil {
		c.log.WarnContext(ctx, "module proposer shadow: propose failed", "err", err)
		return
	}
	// Measure the coverage/floor metrics over the proposer's OWN modules — the
	// synthesized acceptance bookend (a deterministic runtime backstop) covers
	// every floor dim + case id, so measuring the bookended epic would launder any
	// proposer into constant-true. The cutover quality signal must reflect the
	// proposer's slicing, not Orion's backstop. Cluster counts use the FULL plan
	// (that is what actually clusters/builds at cutover).
	raw := decomposer.Epic{Title: pe.Title}
	for _, t := range pe.Tasks {
		if t.Key != "acceptance" {
			raw.Tasks = append(raw.Tasks, t)
		}
	}
	superset, missing := decomposer.CoverageDiff(raw, oracle)
	pc, pcErr := decomposer.Cluster(pe.Tasks)
	oc, ocErr := decomposer.Cluster(oracle.Tasks)
	pcn, ocn := -1, -1 // -1 = uncomputable (never reads as a spurious non-regression)
	if pcErr == nil {
		pcn = len(pc)
	}
	if ocErr == nil {
		ocn = len(oc)
	}
	rec := contextstore.ShadowRecord{
		SpecHash:         es.Hash,
		ProposerModules:  len(raw.Tasks),
		OracleModules:    len(oracle.Tasks),
		ProposerClusters: pcn,
		OracleClusters:   ocn,
		SupersetOK:       superset,
		FloorOK:          decomposer.ReconcileFloor(floor, raw) == nil,
		CoverageGateOK:   decomposer.CoverageGate(es, raw) == nil,
		Missing:          missing,
	}
	if err := c.store.RecordShadowPlan(ctx, projectID, rec); err != nil {
		c.log.WarnContext(ctx, "module proposer shadow: record failed", "err", err)
	}
}

// livePlanOrFallback runs the semantic ModuleProposer as the LIVE plan source
// (or-809 cutover), admitting its epic only through the deterministic trust
// wall: ReconcileFloor + CoverageGate over its RAW modules (the synthesized
// bookend would launder any proposer into constant-true) AND coverage-superset
// vs the oracle. ok=false on ANY failure — the caller keeps the oracle plan;
// the plan itself never fails. Panics are contained like the shadow path.
func (c *Conductor) livePlanOrFallback(ctx context.Context, projectID string, es spec.ExecutableSpec, oracle decomposer.Epic) (pe decomposer.Epic, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			c.log.WarnContext(ctx, "module proposer live: recovered from panic — oracle drives", "panic", r)
			ok = false
		}
	}()
	// The live comparison record keeps the measured window honest across the
	// cutover (regressions after the flip stay visible).
	c.recordShadowPlan(ctx, projectID, es, oracle)

	floor := decomposer.DefaultFloor()
	pe, err := decomposer.Propose(ctx, es, c.gate.ProjectType(), floor, c.proposer)
	if err != nil {
		c.log.WarnContext(ctx, "module proposer live: propose failed — oracle drives", "err", err)
		return decomposer.Epic{}, false
	}
	raw := decomposer.Epic{Title: pe.Title}
	for _, t := range pe.Tasks {
		if t.Key != "acceptance" {
			raw.Tasks = append(raw.Tasks, t)
		}
	}
	if err := decomposer.ReconcileFloor(floor, raw); err != nil {
		c.log.WarnContext(ctx, "module proposer live: floor gate failed — oracle drives", "err", err)
		return decomposer.Epic{}, false
	}
	if err := decomposer.CoverageGate(es, raw); err != nil {
		c.log.WarnContext(ctx, "module proposer live: coverage gate failed — oracle drives", "err", err)
		return decomposer.Epic{}, false
	}
	if superset, missing := decomposer.CoverageDiff(raw, oracle); !superset {
		c.log.WarnContext(ctx, "module proposer live: not a coverage superset of the oracle — oracle drives", "missing", missing)
		return decomposer.Epic{}, false
	}
	c.log.InfoContext(ctx, "module proposer LIVE: proposer plan drives the build", "modules", len(raw.Tasks))
	return pe, true
}

// CutoverView is the `orion plan cutover` projection: the deterministic
// shadow→live cutover criterion evaluated over the project's measured window.
type CutoverView struct {
	Ready      bool   `json:"ready"`
	Reason     string `json:"reason"`
	ShadowRuns int    `json:"shadow_runs"`
	Window     int    `json:"window"`
}

// CutoverStatus evaluates decomposer.CutoverReady over the current project's
// recorded shadow runs (or-809). The flip itself stays a human decision
// (ORION_MODULE_PROPOSER=live); this is the evidence for it.
func (c *Conductor) CutoverStatus(ctx context.Context) (CutoverView, error) {
	if c.store == nil {
		return CutoverView{}, errNoStore
	}
	proj, _, err := c.store.CurrentProjectSpec(ctx)
	if err != nil {
		return CutoverView{}, err
	}
	recs, err := c.store.ShadowPlans(ctx, proj.ID)
	if err != nil {
		return CutoverView{}, err
	}
	outs := make([]decomposer.ShadowOutcome, 0, len(recs))
	for _, r := range recs { // newest first, matching CutoverReady's contract
		outs = append(outs, decomposer.ShadowOutcome{
			SupersetOK: r.SupersetOK, FloorOK: r.FloorOK, CoverageGateOK: r.CoverageGateOK,
			ProposerClusters: r.ProposerClusters, OracleClusters: r.OracleClusters,
		})
	}
	ready, reason := decomposer.CutoverReady(outs, decomposer.CutoverWindow)
	return CutoverView{Ready: ready, Reason: reason, ShadowRuns: len(recs), Window: decomposer.CutoverWindow}, nil
}

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

// ErrPlanStale (or-7et.2 slice 1): the spec was re-ratified AFTER the plan was
// decomposed — the epic's tasks/file-scopes/obligations describe an anchor
// that no longer exists. Every plan read fails LOUD with this instead of
// silently handing the old decomposition to a build that would generate and
// prove against the NEW hash (the worst realignment failure: facts right,
// trajectory wrong). Reconciliation (slice 2) is the recovery path.
var ErrPlanStale = errors.New("plan is stale: the spec was amended and re-ratified after decomposition")

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

// RemoveRequirement drops the behavioral requirement whose ID matches id (its full content-
// addressed id or a unique prefix) from the current DRAFT spec — so the developer can REVISE the
// behavioral contract during the grill, not only append (or-tcs.5: an editable spec). To CHANGE a
// requirement, remove it then add the corrected one. Errors on no match or an ambiguous prefix.
func (c *Conductor) RemoveRequirement(ctx context.Context, id string) error {
	if c.store == nil {
		return errNoStore
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("requirement id is empty")
	}
	_, sp, err := c.currentProjectSpec(ctx)
	if err != nil {
		return fmt.Errorf("no current spec to edit: %w", err)
	}
	reqs := loadRequirements(sp.Requirements)
	match := -1
	for i, r := range reqs {
		if r.ID == id || strings.HasPrefix(r.ID, id) {
			if match >= 0 {
				return fmt.Errorf("requirement id %q is an ambiguous prefix — use a longer id (from list_requirements)", id)
			}
			match = i
		}
	}
	if match < 0 {
		return fmt.Errorf("no requirement matches id %q", id)
	}
	reqs = append(reqs[:match], reqs[match+1:]...)
	b, err := json.Marshal(reqs)
	if err != nil {
		return err
	}
	if err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.Specs().SetRequirements(ctx, sp.ID, string(b))
	}); err != nil {
		return err
	}
	c.log.InfoContext(ctx, "requirement removed", "id", id)
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
		// An approved assumption (or-v9f.19) is a human-confirmed fallback: the
		// decisions table keeps the approval audit record, while every compile
		// path (assemble, recall, plan) sees a fallback_preset — the
		// spec_dimensions CHECK's vocabulary, and one stable anchor hash.
		if d.ValueKind == "assumption_approved" {
			kinds[d.Key] = "fallback_preset"
		}
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
	// Zero-case hard fail (or-8ti.1, the or-y9d false-pass class): a spec with
	// nothing to execute would "prove" vacuously green. Ratification is where
	// that stops — intermediate compiles (preview, decomposer) may pass through
	// case-less states while the developer is still elaborating.
	if len(es.ResponseContract.Cases) == 0 {
		return spec.ExecutableSpec{}, fmt.Errorf(
			"cannot ratify: the spec declares no behavioral case, so nothing can be executed or proven — capture each behavior with add_requirement (>=1 request→response case) before ratifying")
	}
	// Assumption gate (or-v9f.19): a fallback the developer never explicitly
	// confirmed must not ride into the ratified spec on prompt discipline. The
	// approval is a recorded act (approve_assumptions), and ratification is where
	// its absence is caught deterministically.
	if len(fallbacks) > 0 {
		keys := make([]string, 0, len(fallbacks))
		for _, f := range fallbacks {
			keys = append(keys, fmt.Sprintf("%s=%s", f.key, f.value))
		}
		return spec.ExecutableSpec{}, fmt.Errorf(
			"cannot ratify: %d assumption(s) lack the developer's explicit approval: %s — surface each to the developer, then record the confirmation with approve_assumptions",
			len(fallbacks), strings.Join(keys, ", "))
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
	// or-7et.2 slice 1(c): re-ratifying over an existing plan is never SILENT.
	// The amendment is legitimate (the new anchor is the truth), but the plan
	// decomposed from the old anchor is now stale — every plan read fails loud
	// with ErrPlanStale until reconciliation (slice 2) re-anchors it.
	// or-7et.2 slice 2: an amended anchor over an existing plan reconciles
	// IMMEDIATELY — selective invalidation replaces the stale-plan wall.
	c.reconcileAfterRatify(ctx, proj.ID, es.Hash)
	return es, nil
}

// ApproveAssumptions records the developer's explicit confirmation of the
// currently-open fallback assumptions (or-v9f.19): each becomes a stored
// decision with value_kind "assumption_approved" — the audit record the
// ratification gate requires. Call it ONLY after the developer has seen and
// confirmed each assumption. Returns the approved key=value pairs.
func (c *Conductor) ApproveAssumptions(ctx context.Context) ([]string, error) {
	proj, sp, _, fallbacks, err := c.assembleSpec(ctx)
	if err != nil {
		return nil, err
	}
	if len(fallbacks) == 0 {
		return nil, nil
	}
	approved := make([]string, 0, len(fallbacks))
	err = c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		for _, f := range fallbacks {
			if _, e := tx.Decisions().Create(ctx, proj.ID, sp.ID, f.key, f.value, "assumption_approved", false); e != nil {
				return e
			}
			approved = append(approved, f.key+"="+f.value)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("record assumption approvals: %w", err)
	}
	c.log.InfoContext(ctx, "assumptions approved", "count", len(approved))
	return approved, nil
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
	return c.recallSpecFor(ctx, proj, sp)
}

// recallSpecFor re-derives and anchor-verifies the accepted spec for an already-
// resolved (project, spec) pair: it checks the spec is accepted, recompiles from
// the stored decisions, and verifies the hash matches the persisted one (Trust
// invariant 7). Shared by RecallSpec (active slot) and RecallLastProvenSpec
// (delivered fallback) so both apply identical status + anchor checks. The caller
// is responsible for having resolved the completeness gate to the project's type.
func (c *Conductor) recallSpecFor(ctx context.Context, proj contextstore.Project, sp contextstore.Spec) (spec.ExecutableSpec, error) {
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

// RecallLastProvenSpec resolves the spec whose proven code the developer means by
// "show me the code": the active accepted spec if one is in flight, otherwise the
// most recently DELIVERED project's spec. On Accept, delivery moves the project out
// of the active slot (or-v9f.1), so RecallSpec (active-only) can no longer see it —
// yet the just-proven code is exactly what the developer is asking to inspect. This
// fallback keeps `show_code` truthful across the delivery boundary. When there is
// genuinely nothing delivered, the original active-slot error is preserved so the
// "no accepted, proven spec yet" message still surfaces.
func (c *Conductor) RecallLastProvenSpec(ctx context.Context) (spec.ExecutableSpec, error) {
	es, err := c.RecallSpec(ctx)
	if err == nil {
		return es, nil
	}
	if c.store == nil {
		return spec.ExecutableSpec{}, err
	}
	proj, sp, derr := c.store.LastDeliveredProjectSpec(ctx)
	if derr != nil {
		return spec.ExecutableSpec{}, err // nothing delivered: keep the active-slot message
	}
	// Match the completeness gate to the delivered project's type so the recompiled
	// anchor uses the same checklist it was originally compiled under.
	if pt := proj.ProjectType; pt != "" && pt != c.gate.ProjectType() {
		c.mu.Lock()
		c.gate = completeness.NewAnalyzer(pt)
		c.mu.Unlock()
	}
	return c.recallSpecFor(ctx, proj, sp)
}

// PlanTask is one task in the rendered plan.
type PlanTask struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	ProofObligation string   `json:"proof_obligation"`
	FileScope       string   `json:"file_scope"`
	DependsOn       []string `json:"depends_on"`
	// ReproofRequired (or-7et.2 slice 2): the task's covered surface changed in
	// a reconciled amendment — the builder must bypass proof-memo reuse.
	ReproofRequired bool `json:"reproof_required,omitempty"`
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
	// Read path: resolve active-or-last-delivered so `orion plan show` still answers
	// after Accept moves the project out of the active slot (or-v9f.1) — otherwise
	// the plan for the code just built reads as "no current spec".
	proj, sp, err := c.currentOrDeliveredProjectSpec(ctx)
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
	// Fast path: a plan already exists — but ONLY if it was decomposed from THIS
	// anchor (or-7et.2 slice 1). A '' hash is a pre-migration epic, grandfathered.
	if existing, err := c.currentEpic(ctx, proj.ID); err == nil {
		if existing.SpecHash != "" && existing.SpecHash != sp.Hash {
			return "", fmt.Errorf("%w (plan anchored to %.12s, spec is now %.12s) — amend-and-rebuild reconciliation is not yet available; start a fresh project for the amended intent", ErrPlanStale, existing.SpecHash, sp.Hash)
		}
		return existing.ID, nil
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

	// or-809 SHADOW: when a proposer is injected and ORION_MODULE_PROPOSER=shadow,
	// run the semantic proposer ALONGSIDE the oracle (which still drives the build
	// below — byte-identical behavior) and record how its plan compares. Entirely
	// best-effort: any proposer/gate/persist error is logged and never fails the
	// plan (the deterministic oracle is the live plan). Cutover to driving the
	// build off the proposer is gated on this measured window
	// (decomposer.CutoverReady) and flipped by a HUMAN via =live.
	switch mode := os.Getenv("ORION_MODULE_PROPOSER"); {
	case c.proposer != nil && mode == "shadow":
		c.recordShadowPlan(ctx, proj.ID, es, epic)
	case c.proposer != nil && mode == "live":
		// or-809 LIVE: the proposer drives ONLY through the deterministic trust
		// wall — ReconcileFloor + CoverageGate on its RAW modules plus
		// coverage-superset vs the oracle, with Orion's synthesized bookend on
		// top. Any failure falls back to the oracle plan (fail-safe, never
		// fail the plan); the shadow record keeps accruing either way.
		if pe, ok := c.livePlanOrFallback(ctx, proj.ID, es, epic); ok {
			epic = pe
		}
	}

	// IssueReviewGate (or-zn8, V3 Step 4): adversarial review over the FINAL
	// issue set (whichever source produced it), before anything persists —
	// "blocks until patched" in blocking mode, advisory-by-default otherwise.
	if err := c.reviewPlanGate(ctx, proj.ID, es, epic); err != nil {
		return "", err
	}

	// or-56c.2: the design-proof synthesis slot — after spec+STPA, before the
	// plan persists. Best-effort: it drafts a ratifiable artifact, never blocks.
	c.synthesizeDesignModel(ctx, proj.ID, es)

	var epicID string
	err = c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		// Re-check inside the write tx to avoid a double-decompose race.
		if existing, e := tx.Epics().LatestForProject(ctx, proj.ID); e == nil {
			epicID = existing.ID
			return nil
		} else if !errors.Is(e, contextstore.ErrNotFound) {
			return e
		}
		eid, _, err := persistEpicTx(ctx, tx, proj.ID, sp.ID, es.Hash, epic)
		epicID = eid
		return err
	})
	return epicID, err
}

// persistEpicTx writes a decomposed epic (tasks, obligations, deps) anchored to
// a spec hash, returning the epic id and the task key→id map. Shared by
// ensurePlan and plan reconciliation (or-7et.2 slice 2).
func persistEpicTx(ctx context.Context, tx *contextstore.Tx, projectID, specID, specHash string, epic decomposer.Epic) (string, map[string]string, error) {
	eid, err := tx.Epics().Create(ctx, projectID, specID, epic.Title, specHash)
	if err != nil {
		return "", nil, err
	}
	keyToID := map[string]string{}
	for _, task := range epic.Tasks {
		tid, err := tx.Tasks().Create(ctx, eid, task.Title, task.FileScope)
		if err != nil {
			return "", nil, err
		}
		keyToID[task.Key] = tid
		if _, err := tx.ProofObligations().Create(ctx, tid, task.ProofObligation); err != nil {
			return "", nil, err
		}
	}
	for _, task := range epic.Tasks {
		for _, dep := range task.DependsOn {
			if err := tx.Tasks().AddDep(ctx, keyToID[task.Key], keyToID[dep]); err != nil {
				return "", nil, err
			}
		}
	}
	return eid, keyToID, nil
}

// currentEpicID returns the latest epic id for a project (read-only).
func (c *Conductor) latestSpec(ctx context.Context, projectID string) (contextstore.Spec, error) {
	var sp contextstore.Spec
	err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		s, err := tx.Specs().LatestForProject(ctx, projectID)
		if err != nil {
			return err
		}
		sp = s
		return nil
	})
	return sp, err
}

func (c *Conductor) currentEpicID(ctx context.Context, projectID string) (string, error) {
	e, err := c.currentEpic(ctx, projectID)
	return e.ID, err
}

func (c *Conductor) currentEpic(ctx context.Context, projectID string) (contextstore.Epic, error) {
	var epic contextstore.Epic
	err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		e, err := tx.Epics().LatestForProject(ctx, projectID)
		if err != nil {
			return err
		}
		epic = e
		return nil
	})
	return epic, err
}

func (c *Conductor) readPlan(ctx context.Context, proj contextstore.Project) (PlanView, error) {
	// The read path re-checks staleness independently (or-7et.2): callers that
	// skip ensurePlan must still never see a plan the anchor has outlived.
	sp, err := c.latestSpec(ctx, proj.ID)
	if err != nil {
		return PlanView{}, err
	}
	var pv PlanView
	err = c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		epic, err := tx.Epics().LatestForProject(ctx, proj.ID)
		if err != nil {
			return err
		}
		if epic.SpecHash != "" && epic.SpecHash != sp.Hash {
			return fmt.Errorf("%w (plan anchored to %.12s, spec is now %.12s)", ErrPlanStale, epic.SpecHash, sp.Hash)
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
				ReproofRequired: t.ReproofRequired,
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
