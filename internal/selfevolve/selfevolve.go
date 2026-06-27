// Package selfevolve closes the self-evolution loop (or-qau, begins or-lrr): it promotes
// proof-passed memory CANDIDATES — generation-tier, active=false items written by or-ykz.8
// after a passing build — into discoverable, reloadable SKILL.md files in a generation-trust
// skills scope.
//
// Trust: a promoted skill is GENERATION trust. Generation skills are quarantined from proof and
// are reloadable (invariant 8), so a self-evolved skill can never reach a proof prompt — the
// trust wall holds across the loop.
//
// This is DEFAULT OFF: nothing here runs unless a caller explicitly invokes PromoteCandidates
// (e.g. `orion evolve`). SkillEval/regression quality-gating and richer candidates (the
// forked-agent replay) are the next layers on top of this mechanism.
package selfevolve

import (
	"context"
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/skill"
)

// PromoteCandidates materializes every active memory candidate as a generation-tier skill in
// skillsDir, returning the promoted skill names. Idempotent: each skill's name is derived from
// the candidate's content-addressed id, so re-running overwrites rather than duplicating.
func PromoteCandidates(ctx context.Context, mem *memory.Store, skillsDir string) ([]string, error) {
	if mem == nil {
		return nil, nil
	}
	cands, err := mem.ListCandidates(ctx)
	if err != nil {
		return nil, err
	}
	var promoted []string
	for _, c := range cands {
		sk := candidateToSkill(c)
		if _, werr := skill.WriteSkill(skillsDir, sk); werr != nil {
			return promoted, fmt.Errorf("promote candidate %s: %w", c.ID, werr)
		}
		promoted = append(promoted, sk.Name)
	}
	return promoted, nil
}

// candidateToSkill renders a candidate memory item as a generation-tier skill with a stable,
// id-derived name (so promotion is idempotent).
func candidateToSkill(c memory.Item) skill.Skill {
	desc := oneLine(c.Content)
	if desc == "" {
		desc = "A procedure learned from a proof-passed build."
	}
	if len(desc) > 1024 {
		desc = desc[:1024]
	}
	body := c.Content + "\n\nSource: self-evolved candidate (generation trust). Promoted from a " +
		"proof-passed build; review before relying on it."
	return skill.Skill{
		Name:        "learned-" + sanitizeID(c.ID),
		Description: desc,
		Body:        body,
		Trust:       skill.TrustGeneration,
		Metadata:    map[string]string{"orion-source": "self-evolved"},
	}
}

// sanitizeID reduces a candidate id to a valid skill-name segment. Candidate ids are
// sha256[:16] (lowercase hex) — already valid — but this guards against any other id shape.
func sanitizeID(id string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(id) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if s == "" {
		s = "candidate"
	}
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
