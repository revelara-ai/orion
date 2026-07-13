package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
}

// empty reports a doc with no content at all — nothing to steer by.
func (g GoalsDoc) empty() bool {
	return len(g.Goals) == 0 && len(g.NonGoals) == 0 && len(g.SuccessCriteria) == 0
}

// Render formats the doc for developer review and prompt injection.
func (g GoalsDoc) Render() string {
	var b strings.Builder
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
	section("Non-goals", g.NonGoals)
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
	if c.store == nil {
		return errNoStore
	}
	proj, _, err := c.store.CurrentProjectSpec(ctx)
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
	proj, _, err := c.store.CurrentProjectSpec(ctx)
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
	proj, _, err := c.store.CurrentProjectSpec(ctx)
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
