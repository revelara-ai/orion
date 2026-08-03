package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

// V3 Step 5 (or-794, the last migration-ladder step): the GRILL drives
// elicitation open-endedly — relentless listening at INTENT altitude — and
// the fixed completeness checklist is DEMOTED to a reliability floor. The
// floor never goes away: every floor dimension must still resolve before the
// spec compiles (spec.Compile's checklist loop is untouched), and unresolved
// floor dimensions still surface as questions — they just no longer lead.
//
// Staged + reversible like every V3 swap: ORION_ELICITATION=grill activates;
// unset, the checklist drives byte-identically to V2. The grill is an LLM
// (assumed adversarial): a grill error or panic FAILS OPEN to the checklist
// driver, and nothing the grill produces can remove a floor question.

// GrillAgent proposes the next open-ended elicitation questions for an
// intent, given what is already resolved. Empty means the grill has nothing
// left to ask (the floor may still have questions).
type GrillAgent func(ctx context.Context, intent string, resolved map[string]string, floor []completeness.OpenDecision) ([]completeness.OpenDecision, error)

// SetGrillAgent injects the open-ended elicitation driver (or-794). Safe to
// leave nil — the checklist drives, exactly as before.
func (c *Conductor) SetGrillAgent(g GrillAgent) { c.grill = g }

// Researcher resolves a research-mode question by lookup (code, docs, web) and
// returns the finding (or-kjcm.4). An error means the lookup failed — the
// caller downgrades the question to the developer, it never guesses.
type Researcher func(ctx context.Context, question contextstore.OpenQuestion) (string, error)

// SetResearcher injects the research-question resolver. Safe to leave nil —
// research questions then surface to the developer like any other.
func (c *Conductor) SetResearcher(r Researcher) { c.researcher = r }

// grillDrives decides the elicitation driver: the env override always wins
// (staged V3 swap), and a LARGE-scale intent gets the grill BY DEFAULT
// (or-045a.2 — the mech-game dogfood entered checklist mode and was asked
// http-ops questions; a large greenfield needs goal-altitude listening, not a
// port number). Fail-safe: no store or no scale reads as standard.
func (c *Conductor) grillDrives(ctx context.Context) bool {
	if os.Getenv("ORION_ELICITATION") == "grill" {
		return true
	}
	if c.store == nil {
		return false
	}
	proj, _, err := c.store.CurrentProjectSpec(ctx)
	if err != nil {
		return false
	}
	return proj.Scale == completeness.ScaleLarge
}

// openDecisions is THE elicitation driver seam: it returns the questions the
// developer sees. Checklist mode (default): the deterministic floor alone.
// Grill mode: the grill's open-ended questions lead, followed by every floor
// dimension still unresolved — demoted, never dropped.
//
// or-kjcm.3: with a store, grill questions are ARTIFACTS, not utterances —
// generated once per answer-set, persisted to the open-questions ledger, and
// served from it on every subsequent gate read. The LLM is consulted only when
// the answer-set has changed since the last consult; a question resolved
// through record_answer or resolve_open_question keeps its key claimed in the
// ledger, so a re-consult can never re-raise it.
func (c *Conductor) openDecisions(ctx context.Context, intent string, answers map[string]string) (out []completeness.OpenDecision) {
	floor := c.gate.Analyze(intent, answers)
	if !c.grillDrives(ctx) || c.grill == nil {
		return floor
	}
	defer func() {
		if r := recover(); r != nil {
			c.log.WarnContext(ctx, "grill elicitation: recovered from panic — checklist drives", "panic", r)
			out = floor
		}
	}()
	if c.store == nil {
		// Store-less grill mode keeps the direct behavior: consult per read.
		qs, err := c.grill(ctx, intent, answers, floor)
		if err != nil {
			c.log.WarnContext(ctx, "grill elicitation failed — checklist drives", "err", err)
			return floor
		}
		return leadThenFloor(qs, floor, answers)
	}
	proj, _, err := c.currentProjectSpec(ctx)
	if err != nil {
		c.log.WarnContext(ctx, "grill elicitation: no active project — checklist drives", "err", err)
		return floor
	}
	ledger, err := c.grillLedger(ctx, proj.ID)
	if err != nil {
		c.log.WarnContext(ctx, "grill ledger unreadable — checklist drives", "err", err)
		return floor
	}
	if fp := answersFingerprint(answers); c.lastGrillFP(proj.ID) != fp {
		qs, gerr := c.grill(ctx, intent, answers, floor)
		if gerr != nil {
			// The ledger is durable knowledge: an outage serves what was
			// already elicited instead of dropping to the bare floor.
			c.log.WarnContext(ctx, "grill elicitation failed — persisted questions and the floor drive", "err", gerr)
		} else {
			ledger = c.persistGrillQuestions(ctx, proj.ID, ledger, qs)
			c.setLastGrillFP(proj.ID, fp)
		}
	}
	ledger = c.resolveResearchQuestions(ctx, proj.ID, ledger)
	return leadThenFloor(frontierOf(ledger, answers), floor, answers)
}

// frontierOf returns the ledger's OPEN questions whose dependency edges are
// satisfied (or-kjcm.7) — the frontier. A question is held back while any
// blocked_by key is still an open question with no recorded answer: the
// blocking answer changes what the held question even means, so surfacing
// them together invites answering the detail before the scope. Edges to keys
// with no open question and no answer are ignored (fail-open: a dangling edge
// must never strand a question). Order is ledger (creation) order, so the
// served sequence is deterministic regardless of how the grill emitted them.
func frontierOf(ledger []contextstore.OpenQuestion, answers map[string]string) []completeness.OpenDecision {
	openKeys := map[string]bool{}
	for _, q := range ledger {
		if q.Status == "open" && q.Key != "" {
			openKeys[q.Key] = true
		}
	}
	blocked := func(q contextstore.OpenQuestion) bool {
		for _, dep := range strings.Split(q.BlockedBy, ",") {
			dep = strings.TrimSpace(dep)
			if dep == "" || dep == q.Key {
				continue
			}
			if openKeys[dep] && strings.TrimSpace(answers[dep]) == "" {
				return true
			}
		}
		return false
	}
	var out []completeness.OpenDecision
	for _, q := range ledger {
		if q.Status != "open" || blocked(q) {
			continue
		}
		out = append(out, completeness.OpenDecision{Key: q.Key, Dimension: completeness.DimFunctional, Question: q.Question, Mode: q.Mode})
	}
	return out
}

// resolveResearchQuestions burns down the open research-mode questions with
// the injected researcher (or-kjcm.4): a successful lookup resolves the
// question with its finding (and records the keyed decision, so the grill's
// "already resolved" view includes it); a failed lookup DOWNGRADES the
// question to hitl — back to the developer, never a guess. nil researcher =
// no-op: research questions surface to the developer like any other.
func (c *Conductor) resolveResearchQuestions(ctx context.Context, projectID string, ledger []contextstore.OpenQuestion) []contextstore.OpenQuestion {
	if c.researcher == nil {
		return ledger
	}
	_, sp, spErr := c.currentProjectSpec(ctx)
	for i, q := range ledger {
		if q.Status != "open" || q.Mode != "research" {
			continue
		}
		finding, err := c.researcher(ctx, q)
		if err != nil || strings.TrimSpace(finding) == "" {
			c.log.WarnContext(ctx, "research lookup failed — question returns to the developer", "key", q.Key, "err", err)
			if uerr := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
				return tx.OpenQuestions().SetMode(ctx, q.ID, "hitl")
			}); uerr != nil {
				c.log.WarnContext(ctx, "research downgrade not persisted", "key", q.Key, "err", uerr)
				continue
			}
			ledger[i].Mode = "hitl"
			continue
		}
		if err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
			if q.Key != "" && spErr == nil {
				if _, e := tx.Decisions().Create(ctx, projectID, sp.ID, q.Key, finding, "precise", false); e != nil {
					return e
				}
			}
			return tx.OpenQuestions().Resolve(ctx, q.ID, "answered", finding)
		}); err != nil {
			c.log.WarnContext(ctx, "research resolution not persisted", "key", q.Key, "err", err)
			continue
		}
		ledger[i].Status, ledger[i].Resolution = "answered", finding
		c.log.InfoContext(ctx, "research question resolved", "key", q.Key)
	}
	return ledger
}

// leadThenFloor orders the developer-facing set: the open-ended questions
// lead (unkeyed, duplicate, and already-answered ones dropped), then every
// unresolved floor dimension — demoted, never dropped.
func leadThenFloor(lead, floor []completeness.OpenDecision, answers map[string]string) []completeness.OpenDecision {
	var out []completeness.OpenDecision
	seen := map[string]bool{}
	for _, q := range lead {
		key := strings.TrimSpace(q.Key)
		if key == "" || seen[key] {
			continue // an unkeyed or duplicate grill question is unanswerable — drop it
		}
		if strings.TrimSpace(answers[key]) != "" {
			continue // already answered in a prior round
		}
		if q.Dimension == "" {
			q.Dimension = completeness.DimFunctional
		}
		seen[key] = true
		out = append(out, q)
	}
	for _, f := range floor {
		if !seen[f.Key] {
			seen[f.Key] = true
			out = append(out, f)
		}
	}
	return out
}

// grillLedger loads ALL of the project's grill-origin questions, any status —
// the open ones to serve, the resolved ones to dedup against.
func (c *Conductor) grillLedger(ctx context.Context, projectID string) ([]contextstore.OpenQuestion, error) {
	var all []contextstore.OpenQuestion
	err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		all, e = tx.OpenQuestions().ListByProject(ctx, projectID)
		return e
	})
	if err != nil {
		return nil, err
	}
	var out []contextstore.OpenQuestion
	for _, q := range all {
		if q.Origin == "grill" {
			out = append(out, q)
		}
	}
	return out, nil
}

// persistGrillQuestions files the grill's fresh proposals as advisory ledger
// rows, deduped by key against every existing grill question REGARDLESS of
// status — an answered question's key stays claimed forever. Returns the
// updated ledger; a write failure logs and serves what exists (the surfaced
// set must never break on bookkeeping).
func (c *Conductor) persistGrillQuestions(ctx context.Context, projectID string, ledger []contextstore.OpenQuestion, qs []completeness.OpenDecision) []contextstore.OpenQuestion {
	claimed := map[string]bool{}
	for _, q := range ledger {
		claimed[q.Key] = true
	}
	for _, q := range qs {
		key := strings.TrimSpace(q.Key)
		if key == "" || claimed[key] {
			continue
		}
		claimed[key] = true
		row := contextstore.OpenQuestion{
			ProjectID: projectID, Phase: "spec", Origin: "grill", Key: key,
			Question: strings.TrimSpace(q.Question), Severity: "advisory", Mode: q.Mode,
			BlockedBy: strings.Join(q.BlockedBy, ","),
		}
		if row.Mode == "" {
			row.Mode = "hitl" // mirror the store default so the in-memory ledger matches
		}
		err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
			id, e := tx.OpenQuestions().Create(ctx, row)
			row.ID, row.Status = id, "open"
			return e
		})
		if err != nil {
			c.log.WarnContext(ctx, "grill question not persisted", "key", key, "err", err)
			continue
		}
		ledger = append(ledger, row)
	}
	return ledger
}

// answersFingerprint identifies an answer-set: the grill is consulted at most
// once per distinct set (or-kjcm.3 — gate reads must not each burn an LLM
// call, and must return the same questions).
func answersFingerprint(answers map[string]string) string {
	keys := make([]string, 0, len(answers))
	for k := range answers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(answers[k]))
		h.Write([]byte{1})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// lastGrillFP / setLastGrillFP track, per project, the answer-set the grill
// last ran against. Process-local by design: after a restart the first read
// re-consults once, and the ledger's key dedup keeps that idempotent.
func (c *Conductor) lastGrillFP(projectID string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.grillFP[projectID]
}

func (c *Conductor) setLastGrillFP(projectID, fp string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.grillFP == nil {
		c.grillFP = map[string]string{}
	}
	c.grillFP[projectID] = fp
}
