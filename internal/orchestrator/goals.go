package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// GoalsDoc is the goal-altitude artifact of a large greenfield intake
// (or-045a.2): what the project IS for, what it is explicitly NOT for, and
// how success is judged — ratified BEFORE the spec, anchored in the context
// store (never a loose file; the mech-game dogfood free-wrote goals.md into
// the harness cwd where nothing could ratify or recall it).
type GoalsDoc struct {
	Goals           []string `json:"goals"`
	NonGoals        []string `json:"non_goals"`
	SuccessCriteria []string `json:"success_criteria"`
	// Destination (or-kjcm.6) is the one-line answer to "what does reaching
	// the end of this effort look like?" — recorded for EVERY scale, not just
	// the large rail, because it fixes what every elicitation question means.
	// It steers the grill even as a draft: it is the developer's own words.
	Destination string `json:"destination,omitempty"`
	// NonGoalDetails (or-kjcm.6) is the structured out-of-scope ledger: each
	// exclusion carries its why, so a later "should we add X?" reads the
	// reason, not just the ruling. NonGoals stays the legacy statement list
	// (kept in sync by ProposeGoals) so older documents and readers work.
	NonGoalDetails []NonGoal `json:"non_goal_details,omitempty"`
}

// NonGoal is one consciously-excluded piece of work and the reason it was
// ruled out — a scoping act with its rationale attached.
type NonGoal struct {
	Statement string `json:"statement"`
	Rationale string `json:"rationale,omitempty"`
}

// ScopeConflict names the ratified non-goal a proposed requirement crosses,
// and why the checker thinks so.
type ScopeConflict struct {
	NonGoal string
	Reason  string
}

// ScopeChecker judges one requirement against the ratified non-goals
// (or-kjcm.6). nil result = in scope. An error means the check itself failed
// — the caller fails OPEN (records the requirement), never blocks capture on
// a broken judge.
type ScopeChecker func(ctx context.Context, requirement string, nonGoals []NonGoal) (*ScopeConflict, error)

// SetScopeChecker injects the scope judge. Safe to leave nil — non-goals are
// then advisory prose, exactly as before.
func (c *Conductor) SetScopeChecker(s ScopeChecker) { c.scopeCheck = s }

// empty reports a doc with no content at all — nothing to steer by.
func (g GoalsDoc) empty() bool {
	return len(g.Goals) == 0 && len(g.NonGoals) == 0 && len(g.NonGoalDetails) == 0 &&
		len(g.SuccessCriteria) == 0 && strings.TrimSpace(g.Destination) == ""
}

// normalize syncs the two non-goal representations: structured entries are
// authoritative when present (statements derived); bare statements gain
// rationale-less entries so NonGoalLedger always has the full set.
func (g *GoalsDoc) normalize() {
	if len(g.NonGoalDetails) > 0 {
		g.NonGoals = g.NonGoals[:0]
		for _, n := range g.NonGoalDetails {
			g.NonGoals = append(g.NonGoals, n.Statement)
		}
		return
	}
	for _, s := range g.NonGoals {
		g.NonGoalDetails = append(g.NonGoalDetails, NonGoal{Statement: s})
	}
}

// NonGoalLedger returns the structured exclusions (statement + why),
// synthesizing entries for legacy rationale-less documents.
func (g GoalsDoc) NonGoalLedger() []NonGoal {
	if len(g.NonGoalDetails) > 0 {
		return g.NonGoalDetails
	}
	out := make([]NonGoal, 0, len(g.NonGoals))
	for _, s := range g.NonGoals {
		out = append(out, NonGoal{Statement: s})
	}
	return out
}

// Render formats the doc for developer review and prompt injection.
func (g GoalsDoc) Render() string {
	var b strings.Builder
	if strings.TrimSpace(g.Destination) != "" {
		b.WriteString("Destination: " + g.Destination + "\n")
	}
	section := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		b.WriteString(title + ":\n")
		for _, it := range items {
			b.WriteString("- " + it + "\n")
		}
	}
	section("Goals", g.Goals)
	if ngs := g.NonGoalLedger(); len(ngs) > 0 {
		b.WriteString("Non-goals:\n")
		for _, n := range ngs {
			b.WriteString("- " + n.Statement)
			if n.Rationale != "" {
				b.WriteString(" (why: " + n.Rationale + ")")
			}
			b.WriteString("\n")
		}
	}
	section("Success criteria", g.SuccessCriteria)
	return strings.TrimRight(b.String(), "\n")
}

// ProposeGoals persists the LLM-drafted goals document as the active
// project's DRAFT (re-proposing re-opens ratification). The developer
// ratifies with RatifyGoals — LLM proposes, the gate verifies.
func (c *Conductor) ProposeGoals(ctx context.Context, doc GoalsDoc) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if doc.empty() {
		return fmt.Errorf("propose_goals: the document is empty — draft at least one goal, non-goal, or success criterion from the conversation")
	}
	doc.normalize()
	if c.store == nil {
		return errNoStore
	}
	proj, _, err := c.activeProjectSpec(ctx)
	if err != nil {
		return fmt.Errorf("propose_goals: no active project: %w", err)
	}
	content, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.Goals().Upsert(ctx, proj.ID, string(content))
	}); err != nil {
		return err
	}
	c.log.InfoContext(ctx, "goals proposed", "goals", len(doc.Goals), "non_goals", len(doc.NonGoals))
	return nil
}

// RatifyGoals anchors the active project's proposed goals with a content hash
// (the developer's confirmation is the ratifying act). Refused when nothing
// was proposed.
func (c *Conductor) RatifyGoals(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.store == nil {
		return "", errNoStore
	}
	proj, _, err := c.activeProjectSpec(ctx)
	if err != nil {
		return "", fmt.Errorf("ratify_goals: no active project: %w", err)
	}
	// Open-question gate (or-045a.6): a blocking goals-phase ambiguity must be
	// answered or explicitly assumed before the goals anchor — deferring a
	// question must never let it vanish into a ratified document.
	if qs := c.blockingOpenQuestions(ctx, proj.ID, "goals"); len(qs) > 0 {
		return "", fmt.Errorf("cannot ratify goals: %d blocking open question(s) — answer or explicitly assume each (resolve_open_question):%s", len(qs), renderOpenQuestions(qs))
	}
	var hash string
	if err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		g, e := tx.Goals().Get(ctx, proj.ID)
		if e != nil {
			return fmt.Errorf("ratify_goals: nothing proposed yet — draft the goals with propose_goals first: %w", e)
		}
		sum := sha256.Sum256([]byte(g.Content))
		hash = hex.EncodeToString(sum[:])
		return tx.Goals().Ratify(ctx, proj.ID, hash)
	}); err != nil {
		return "", err
	}
	c.log.InfoContext(ctx, "goals ratified", "hash", hash)
	return hash, nil
}

// Goals returns the active project's goals document with its status and hash
// (ErrNotFound wrapped when none proposed).
func (c *Conductor) Goals(ctx context.Context) (GoalsDoc, string, string, error) {
	if c.store == nil {
		return GoalsDoc{}, "", "", errNoStore
	}
	proj, _, err := c.activeProjectSpec(ctx)
	if err != nil {
		return GoalsDoc{}, "", "", err
	}
	var g contextstore.Goals
	if err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		g, e = tx.Goals().Get(ctx, proj.ID)
		return e
	}); err != nil {
		return GoalsDoc{}, "", "", err
	}
	var doc GoalsDoc
	if err := json.Unmarshal([]byte(g.Content), &doc); err != nil {
		return GoalsDoc{}, "", "", fmt.Errorf("goals content corrupt: %w", err)
	}
	return doc, g.Status, g.Hash, nil
}

// GoalsSummary renders the RATIFIED goals for prompt injection ("" when none
// are ratified — a draft steers nothing until the developer confirms it).
func (c *Conductor) GoalsSummary(ctx context.Context) string {
	doc, status, _, err := c.Goals(ctx)
	if err != nil || status != "ratified" {
		return ""
	}
	return doc.Render()
}

// GrillContext feeds the grill's steering block (or-kjcm.6): the full RATIFIED
// goals when they exist; otherwise the destination line alone, even from a
// draft — it is the developer's own words, recorded verbatim, and a standard-
// scale intake deserves a steering signal without the full goals ceremony.
func (c *Conductor) GrillContext(ctx context.Context) string {
	doc, status, _, err := c.Goals(ctx)
	if err != nil {
		return ""
	}
	if status == "ratified" {
		return doc.Render()
	}
	if strings.TrimSpace(doc.Destination) != "" {
		return "Destination: " + doc.Destination
	}
	return ""
}

// SetDestination records the one-line destination for the active project at
// ANY scale (or-kjcm.6) — upserted into the goals document draft, preserving
// whatever else is already drafted. Ratified goals are not reopened by it:
// changing a ratified document's destination goes through propose_goals →
// ratify_goals like any other goals edit.
func (c *Conductor) SetDestination(ctx context.Context, line string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.store == nil {
		return errNoStore
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return fmt.Errorf("set_destination: the destination line is empty")
	}
	doc, status, _, err := c.Goals(ctx)
	if err != nil && !errors.Is(err, contextstore.ErrNotFound) {
		return err
	}
	if status == "ratified" {
		return fmt.Errorf("set_destination: the goals document is ratified — revise it with propose_goals and re-ratify instead")
	}
	doc.Destination = line
	return c.ProposeGoals(ctx, doc)
}
