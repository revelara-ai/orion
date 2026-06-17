package backlog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/repos"
)

// PreflightAssessor wraps llm.Generator + the preflight cache to
// decide whether an issue is "in-scope-for-code-change" per SPEC
// §8.4 rule 8. The decision is cached on (issue_id, body_signature)
// per §8.5 so we don't pay LLM cost on every ingest tick; the body
// signature is sha256(title || description) so a body change
// invalidates the cache automatically.
type PreflightAssessor struct {
	// LLM is the model that returns the decision. Required.
	LLM llm.Generator

	// Cache stores decisions across ingest ticks. Required.
	Cache *repos.PreflightCacheRepo

	// Model is the LLM model identifier passed in the request; the
	// llm.Client wires its own default but tests can override.
	Model string

	// Prompter, when non-nil, builds the User text for the LLM
	// request from (title, description). Production wires the
	// default in-scope-or-out-of-scope prompt; tests can override.
	Prompter func(title, description string) string
}

// PreflightResult is the assessor's per-call return value.
type PreflightResult struct {
	Decision repos.PreflightDecision
	Reason   string
	Cached   bool // true when served from cache
}

// Assess returns the cached or freshly-computed decision for
// (issueID, title, description). Cache misses call the LLM and
// persist the result before returning.
func (a *PreflightAssessor) Assess(ctx context.Context, issueID uuid.UUID, title, description string) (PreflightResult, error) {
	if a == nil || a.LLM == nil || a.Cache == nil {
		return PreflightResult{}, errors.New("backlog: preflight assessor not wired")
	}
	bodySig := BodySignature(title, description)

	// Cache hit short-circuits.
	if entry, err := a.Cache.Get(ctx, issueID, bodySig); err == nil {
		return PreflightResult{Decision: entry.Decision, Reason: entry.Reason, Cached: true}, nil
	} else if !errors.Is(err, repos.ErrNotFound) {
		return PreflightResult{}, err
	}

	// Cache miss: ask the model.
	prompter := a.Prompter
	if prompter == nil {
		prompter = defaultPreflightPrompt
	}
	resp, err := a.LLM.Generate(ctx, llm.GenerateRequest{
		System: preflightSystemInstruction,
		User:   prompter(title, description),
	})
	if err != nil {
		return PreflightResult{}, fmt.Errorf("backlog: preflight llm: %w", err)
	}
	decision, reason := parsePreflightResponse(resp.Text)

	// Persist (best-effort: a cache write failure shouldn't fail
	// the ingest tick — surface as wrapped error so the driver can
	// log it but treat the live decision as authoritative).
	if err := a.Cache.Set(ctx, repos.PreflightCacheEntry{
		IssueID:       issueID,
		BodySignature: bodySig,
		Decision:      decision,
		Reason:        reason,
	}); err != nil {
		return PreflightResult{Decision: decision, Reason: reason}, fmt.Errorf("backlog: preflight cache write: %w", err)
	}
	return PreflightResult{Decision: decision, Reason: reason}, nil
}

// BodySignature is sha256(title || "\n" || description) hex-encoded.
// Cache key invalidates automatically when either field changes.
func BodySignature(title, description string) string {
	h := sha256.New()
	h.Write([]byte(title))
	h.Write([]byte{'\n'})
	h.Write([]byte(description))
	return hex.EncodeToString(h.Sum(nil))
}

const preflightSystemInstruction = `You are a triage classifier. Given a tracker issue's title and description, decide whether the issue is "in-scope-for-code-change" — i.e. it has enough specification for an autonomous coding agent to plausibly attempt synthesis.

Reply with exactly one of:
  in_scope: <one-line reason>
  out_of_scope: <one-line reason>

Out-of-scope examples: discussion threads with no concrete ask, design tradeoff decisions requiring human judgment, vague feedback, requests requiring product input. In-scope examples: bug reports with reproduction steps, feature requests with clear acceptance criteria, refactoring tasks with named code paths.`

func defaultPreflightPrompt(title, description string) string {
	return fmt.Sprintf("Title: %s\n\nDescription:\n%s", title, description)
}

// parsePreflightResponse extracts the decision + reason from the
// LLM's free-form reply. Robust to leading whitespace and case.
func parsePreflightResponse(text string) (repos.PreflightDecision, string) {
	t := strings.TrimSpace(text)
	lower := strings.ToLower(t)
	switch {
	case strings.HasPrefix(lower, "in_scope"):
		return repos.PreflightInScope, trimDecisionPrefix(t, len("in_scope"))
	case strings.HasPrefix(lower, "out_of_scope"):
		return repos.PreflightOutOfScope, trimDecisionPrefix(t, len("out_of_scope"))
	}
	// Default to out_of_scope when the model didn't follow the
	// rubric — safer to surface for human review than to claim.
	return repos.PreflightOutOfScope, "unparseable response: " + truncatePreflight(t, 120)
}

func trimDecisionPrefix(t string, prefixLen int) string {
	rest := strings.TrimSpace(t[prefixLen:])
	rest = strings.TrimPrefix(rest, ":")
	return strings.TrimSpace(rest)
}

func truncatePreflight(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
