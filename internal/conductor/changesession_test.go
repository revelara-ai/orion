package conductor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"

	"github.com/revelara-ai/orion/internal/proof/newbehavior"
)

func ratifiedSession() *changeSession {
	return &changeSession{
		intent:     "add a Note helper",
		cases:      []newbehavior.Case{{ID: "c1"}, {ID: "c2"}},
		supersedes: []string{"TestOld"},
		ratified:   true,
	}
}

// TestChangeSessionPendingLifecycle (or-2l7): 'ratified && !consumed' is a
// PENDING oracle; a committed build spends it; a fresh or unratified session
// is never pending.
func TestChangeSessionPendingLifecycle(t *testing.T) {
	cs := ratifiedSession()
	if !cs.pending() {
		t.Fatal("a ratified, unbuilt oracle is pending")
	}
	cs.mu.Lock()
	cs.consumed = true
	cs.mu.Unlock()
	if cs.pending() {
		t.Fatal("a committed (consumed) oracle is spent, not pending")
	}
	if (&changeSession{}).pending() {
		t.Fatal("an empty session is not pending")
	}
	un := ratifiedSession()
	un.ratified = false
	if un.pending() {
		t.Fatal("an unratified draft is not pending")
	}
}

// TestChangeSessionPendingDigest (or-2l7): the post-compaction re-injection
// names the intent, the ratified case ids, and the supersedes — and is empty
// for spent/absent oracles.
func TestChangeSessionPendingDigest(t *testing.T) {
	cs := ratifiedSession()
	d := cs.pendingDigest()
	for _, want := range []string{"IN-FLIGHT CHANGE", "add a Note helper", "c1", "c2", "TestOld", "build_change"} {
		if !strings.Contains(d, want) {
			t.Fatalf("digest must carry %q, got:\n%s", want, d)
		}
	}
	cs.mu.Lock()
	cs.consumed = true
	cs.mu.Unlock()
	if cs.pendingDigest() != "" {
		t.Fatal("a spent oracle re-injects nothing")
	}
}

// TestSubmitChangeIntentGuardsPendingOracle (or-2l7 acceptance): a pending
// (ratified, unbuilt) oracle refuses a silent re-submit, confirm_discard=true
// discards deliberately, and a consumed (post-build) session opens cleanly.
func TestSubmitChangeIntentGuardsPendingOracle(t *testing.T) {
	ctx := context.Background()
	repo := initManagedRepo(t)
	t.Chdir(repo) // repoMap grounds against the cwd repo

	cs := ratifiedSession()
	r := specTools(orchestrator.NewWithStore(openStore(t)), nil, cs, nil)
	submit, ok := r.Get("submit_change_intent")
	if !ok {
		t.Fatal("submit_change_intent not registered")
	}

	// Pending oracle → refuse with the remedy named.
	_, err := submit.Run(ctx, json.RawMessage(`{"intent":"a different change"}`))
	if err == nil || !strings.Contains(err.Error(), "ratified-but-unbuilt oracle") || !strings.Contains(err.Error(), "confirm_discard") {
		t.Fatalf("a pending oracle must refuse a silent re-submit with the remedy: %v", err)
	}
	if !cs.pending() {
		t.Fatal("the refused submit must leave the pending oracle intact")
	}

	// Deliberate discard proceeds and resets the session.
	if _, err := submit.Run(ctx, json.RawMessage(`{"intent":"a different change","confirm_discard":true}`)); err != nil {
		t.Fatalf("confirm_discard must proceed: %v", err)
	}
	if cs.pending() {
		t.Fatal("the deliberate discard must reset the session")
	}

	// Post-build flow: a consumed oracle never trips the guard.
	cs2 := ratifiedSession()
	cs2.consumed = true
	r2 := specTools(orchestrator.NewWithStore(openStore(t)), nil, cs2, nil)
	submit2, _ := r2.Get("submit_change_intent")
	if _, err := submit2.Run(ctx, json.RawMessage(`{"intent":"the next change"}`)); err != nil {
		t.Fatalf("a spent oracle must open the next change cleanly: %v", err)
	}
	if got, _, _, ratified := cs2.snapshot(); got != "the next change" || ratified {
		t.Fatalf("the new change must reset the session: intent=%q ratified=%v", got, ratified)
	}
}
