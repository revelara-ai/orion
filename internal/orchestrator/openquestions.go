package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
)

var (
	openQuestionPhases     = map[string]bool{"goals": true, "stpa": true, "direction": true, "spec": true}
	openQuestionSeverities = map[string]bool{"blocking": true, "advisory": true}
)

// RaiseOpenQuestion persists an intake ambiguity as a first-class question
// (or-045a.6): a grill question the developer deferred, a goal-doc ambiguity
// the conductor flagged, an unanswered direction. It no longer vanishes when
// the conversation moves on — blocking questions gate ratification until they
// are answered or explicitly assumed.
func (c *Conductor) RaiseOpenQuestion(ctx context.Context, phase, origin, key, question, severity string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.store == nil {
		return "", errNoStore
	}
	if !openQuestionPhases[phase] {
		return "", fmt.Errorf("raise_open_question: unknown phase %q (goals|stpa|direction|spec)", phase)
	}
	if !openQuestionSeverities[severity] {
		return "", fmt.Errorf("raise_open_question: unknown severity %q (blocking|advisory)", severity)
	}
	if strings.TrimSpace(question) == "" {
		return "", fmt.Errorf("raise_open_question: the question text is empty")
	}
	proj, _, err := c.currentProjectSpec(ctx)
	if err != nil {
		return "", err
	}
	var id string
	err = c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		id, e = tx.OpenQuestions().Create(ctx, contextstore.OpenQuestion{
			ProjectID: proj.ID, Phase: phase, Origin: origin, Key: strings.TrimSpace(key),
			Question: strings.TrimSpace(question), Severity: severity,
		})
		return e
	})
	if err != nil {
		return "", err
	}
	c.log.InfoContext(ctx, "open question raised", "phase", phase, "severity", severity)
	return id, nil
}

// OpenQuestions lists the active project's OPEN questions.
func (c *Conductor) OpenQuestions(ctx context.Context) ([]contextstore.OpenQuestion, error) {
	if c.store == nil {
		return nil, errNoStore
	}
	proj, _, err := c.currentProjectSpec(ctx)
	if err != nil {
		return nil, err
	}
	var qs []contextstore.OpenQuestion
	err = c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		qs, e = tx.OpenQuestions().ListOpen(ctx, proj.ID)
		return e
	})
	return qs, err
}

// ResolveOpenQuestion is the ONLY way a question leaves the open ledger
// (or-045a.6, no silent path): "answered" records the developer's answer (and
// when the question carries a decision key, the answer lands in the decision
// lineage via RecordAnswer); "assumed" records an APPROVED assumption in the
// same or-v9f.19 ledger ApproveAssumptions audits — no second gate.
func (c *Conductor) ResolveOpenQuestion(ctx context.Context, id, status, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.store == nil {
		return errNoStore
	}
	if status != "answered" && status != "assumed" {
		return fmt.Errorf("resolve_open_question: %q is not a resolution — answer it (answered) or record the developer's explicit assumption (assumed)", status)
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("resolve_open_question: a resolution needs the answer/assumption text")
	}
	proj, sp, err := c.currentProjectSpec(ctx)
	if err != nil {
		return err
	}
	var q contextstore.OpenQuestion
	if err := c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		q, e = tx.OpenQuestions().Get(ctx, id)
		return e
	}); err != nil {
		return fmt.Errorf("resolve_open_question: %w", err)
	}
	if q.ProjectID != proj.ID {
		return fmt.Errorf("resolve_open_question: question %s belongs to another project", id)
	}
	return c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		if q.Key != "" {
			kind := "precise"
			if status == "assumed" {
				kind = "assumption_approved" // the same audit trail or-v9f.19 reads
			}
			if _, e := tx.Decisions().Create(ctx, proj.ID, sp.ID, q.Key, value, kind, false); e != nil {
				return e
			}
		}
		if status == "assumed" {
			// Every assumption is an audited act — gold-labeled exactly like
			// ApproveAssumptions' approvals (or-gb1.8).
			m, v := c.producerProvenance()
			if _, e := tx.GoldLabels().Create(ctx, proj.ID, "assumption", "accept", sp.ID, q.Question+" => "+value, m, v); e != nil {
				return e
			}
		}
		return tx.OpenQuestions().Resolve(ctx, id, status, value)
	})
}

// blockingOpenQuestions returns the active project's open BLOCKING questions,
// optionally filtered by phase ("" = all phases).
func (c *Conductor) blockingOpenQuestions(ctx context.Context, projectID, phase string) []contextstore.OpenQuestion {
	var out []contextstore.OpenQuestion
	_ = c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		qs, e := tx.OpenQuestions().ListOpen(ctx, projectID)
		if e != nil {
			return e
		}
		for _, q := range qs {
			if q.Severity == "blocking" && (phase == "" || q.Phase == phase) {
				out = append(out, q)
			}
		}
		return nil
	})
	return out
}

// renderOpenQuestions formats a refusal payload's question list.
func renderOpenQuestions(qs []contextstore.OpenQuestion) string {
	var b strings.Builder
	for _, q := range qs {
		fmt.Fprintf(&b, "\n  - [%s/%s] %s", q.Phase, q.ID[:8], q.Question)
	}
	return b.String()
}
