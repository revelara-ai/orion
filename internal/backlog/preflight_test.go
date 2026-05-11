package backlog

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/revelara-ai/orion/internal/database"
	"github.com/revelara-ai/orion/internal/repos"
)

func TestPreflight_CacheMissCallsLLMAndPersists(t *testing.T) {
	rls := newRLSPool(t)
	orgID := uuid.New()
	ctx := database.WithRLSContext(context.Background(), "u", orgID, nil)
	_, bindingID := seedRepoBinding(t, rls, ctx, "test/prerepo")

	// Seed a normalized_issue so the preflight cache FK has
	// something to point at.
	issueRepo := repos.NewNormalizedIssueRepo(rls)
	upserted, err := issueRepo.Upsert(ctx, repos.NormalizedIssue{
		RepoID:           repoIDFromBinding(t, rls, ctx, bindingID),
		TrackerBindingID: bindingID,
		ExternalID:       "gh:test/prerepo#1",
		ExternalURL:      "https://example/issues/1",
		Title:            "Fix retry hygiene",
		Description:      "Add bounded retries in worker.go",
		State:            repos.StateOpen,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	gen := &stubLLMGen{resp: "in_scope: clear ask in worker.go"}
	pre := &PreflightAssessor{
		LLM:   gen,
		Cache: repos.NewPreflightCacheRepo(rls),
	}

	res, err := pre.Assess(ctx, upserted.ID, "Fix retry hygiene", "Add bounded retries in worker.go")
	if err != nil {
		t.Fatalf("first Assess: %v", err)
	}
	if res.Cached {
		t.Error("first call should NOT be cached")
	}
	if res.Decision != repos.PreflightInScope {
		t.Errorf("decision=%q want in_scope", res.Decision)
	}

	// Second call: same body signature; should hit cache and NOT
	// call the LLM again. Swap the LLM's response to verify the
	// cache short-circuits.
	gen.resp = "out_of_scope: should NOT see this"
	res2, err := pre.Assess(ctx, upserted.ID, "Fix retry hygiene", "Add bounded retries in worker.go")
	if err != nil {
		t.Fatalf("second Assess: %v", err)
	}
	if !res2.Cached {
		t.Error("second call should hit cache")
	}
	if res2.Decision != repos.PreflightInScope {
		t.Errorf("cached decision=%q want in_scope (cache should win over new LLM resp)", res2.Decision)
	}

	// Different body invalidates the cache.
	res3, err := pre.Assess(ctx, upserted.ID, "Fix retry hygiene", "DIFFERENT body — should miss cache")
	if err != nil {
		t.Fatalf("third Assess: %v", err)
	}
	if res3.Cached {
		t.Error("changed body should miss cache")
	}
	if res3.Decision != repos.PreflightOutOfScope {
		t.Errorf("re-asked decision=%q want out_of_scope", res3.Decision)
	}
}

func TestPreflight_BodySignatureStable(t *testing.T) {
	a := BodySignature("t", "d")
	b := BodySignature("t", "d")
	if a != b {
		t.Errorf("not stable: %s != %s", a, b)
	}
	if a == BodySignature("t", "d2") {
		t.Error("different body produced same sig")
	}
	if a == BodySignature("t2", "d") {
		t.Error("different title produced same sig")
	}
}

func TestPreflight_ParseUnparseableDefaultsOutOfScope(t *testing.T) {
	dec, reason := parsePreflightResponse("yes please")
	if dec != repos.PreflightOutOfScope {
		t.Errorf("got %q, want out_of_scope (default)", dec)
	}
	if reason == "" {
		t.Error("expected non-empty reason for unparseable response")
	}
}
