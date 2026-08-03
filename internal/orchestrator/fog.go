package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// FogQuestion is one sharp question graduating out of a parked area
// (or-kjcm.5): the moment a note can be phrased precisely, it stops being fog
// and becomes a first-class open question on the ledger.
type FogQuestion struct {
	Phase    string `json:"phase"`    // goals | stpa | direction | spec
	Key      string `json:"key"`      // optional decision key
	Question string `json:"question"` // the now-precise question
	Severity string `json:"severity"` // blocking | advisory
	Mode     string `json:"mode"`     // hitl | research ("" = hitl)
}

// ParkFog records a known-unknown too dim to phrase as a question yet. It is
// advisory by construction — fog NEVER gates ratification — and exists so the
// conductor has somewhere to put "a decision about X is coming" other than
// asking prematurely or dropping it.
func (c *Conductor) ParkFog(ctx context.Context, note string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.store == nil {
		return "", errNoStore
	}
	if strings.TrimSpace(note) == "" {
		return "", fmt.Errorf("park_fog: the note is empty")
	}
	proj, _, err := c.currentProjectSpec(ctx)
	if err != nil {
		return "", err
	}
	var id string
	err = c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		id, e = tx.Fog().Create(ctx, proj.ID, strings.TrimSpace(note))
		return e
	})
	if err != nil {
		return "", err
	}
	c.log.InfoContext(ctx, "fog parked", "id", id)
	return id, nil
}

// FogNotes lists the active project's parked areas.
func (c *Conductor) FogNotes(ctx context.Context) ([]contextstore.FogNote, error) {
	if c.store == nil {
		return nil, errNoStore
	}
	proj, _, err := c.currentProjectSpec(ctx)
	if err != nil {
		return nil, err
	}
	var fs []contextstore.FogNote
	err = c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		fs, e = tx.Fog().ListOpen(ctx, proj.ID)
		return e
	})
	return fs, err
}

// GraduateFog converts a parked area into one or more precise open questions,
// atomically: the questions are raised on the ledger and the fog note leaves
// it (status graduated, resolution pointing at the new question ids). The
// test for graduating is being able to phrase the question precisely NOW —
// not being able to answer it.
func (c *Conductor) GraduateFog(ctx context.Context, id string, qs []FogQuestion) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.store == nil {
		return nil, errNoStore
	}
	if len(qs) == 0 {
		return nil, fmt.Errorf("graduate_fog: at least one precise question is required — if the area is moot, dismiss it instead")
	}
	for _, q := range qs {
		if !openQuestionPhases[q.Phase] {
			return nil, fmt.Errorf("graduate_fog: unknown phase %q (goals|stpa|direction|spec)", q.Phase)
		}
		if !openQuestionSeverities[q.Severity] {
			return nil, fmt.Errorf("graduate_fog: unknown severity %q (blocking|advisory)", q.Severity)
		}
		if q.Mode != "" && q.Mode != "hitl" && q.Mode != "research" {
			return nil, fmt.Errorf("graduate_fog: unknown mode %q (hitl|research)", q.Mode)
		}
		if strings.TrimSpace(q.Question) == "" {
			return nil, fmt.Errorf("graduate_fog: a graduated question must be phrased")
		}
	}
	proj, _, err := c.currentProjectSpec(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	err = c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		f, e := tx.Fog().Get(ctx, id)
		if e != nil {
			return fmt.Errorf("graduate_fog: %w", e)
		}
		if f.ProjectID != proj.ID {
			return fmt.Errorf("graduate_fog: note %s belongs to another project", id)
		}
		for _, q := range qs {
			qid, e := tx.OpenQuestions().Create(ctx, contextstore.OpenQuestion{
				ProjectID: proj.ID, Phase: q.Phase, Origin: "fog", Key: strings.TrimSpace(q.Key),
				Question: strings.TrimSpace(q.Question), Severity: q.Severity, Mode: q.Mode,
			})
			if e != nil {
				return e
			}
			ids = append(ids, qid)
		}
		return tx.Fog().Resolve(ctx, id, "graduated", "questions: "+strings.Join(ids, ", "))
	})
	if err != nil {
		return nil, err
	}
	c.log.InfoContext(ctx, "fog graduated", "id", id, "questions", len(ids))
	return ids, nil
}

// DismissFog rules a parked area moot (answered by events, or out of scope)
// with an explicit reason — fog never just evaporates.
func (c *Conductor) DismissFog(ctx context.Context, id, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.store == nil {
		return errNoStore
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("dismiss_fog: a dismissal needs its reason")
	}
	proj, _, err := c.currentProjectSpec(ctx)
	if err != nil {
		return err
	}
	return c.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		f, e := tx.Fog().Get(ctx, id)
		if e != nil {
			return fmt.Errorf("dismiss_fog: %w", e)
		}
		if f.ProjectID != proj.ID {
			return fmt.Errorf("dismiss_fog: note %s belongs to another project", id)
		}
		return tx.Fog().Resolve(ctx, id, "dismissed", strings.TrimSpace(reason))
	})
}
