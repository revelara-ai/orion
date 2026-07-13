package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/proof"
)

func checkpointFixture(t *testing.T, clusters int) (*checkpointer, *[]PhaseEvent) {
	t.Helper()
	store, pid := surfaceStore(t)
	var mu sync.Mutex
	var events []PhaseEvent
	sink := PhaseSink(func(e PhaseEvent) { events = append(events, e) })
	members := map[string][]string{}
	var required []string
	for i := 0; i < clusters; i++ {
		key := fmt.Sprintf("cl%d", i)
		members[key] = []string{key + "-t"}
		required = append(required, fmt.Sprintf("case%d", i))
	}
	cp := newCheckpointer(store, &mu, pid, "run-cp", sink, required, members, func() int { return 0 })
	return cp, &events
}

func acceptFor(caseID string) proof.Report {
	var r proof.Report
	r.ObligationResults = map[string]proof.ObligationResult{caseID: {Executed: true, Passed: true}}
	return r
}

// TestCheckpointCadenceAndDigest (or-v9f.26 acceptance): 8 clusters with k=2
// emit digests after clusters 2/4/6 (never at the end — delivery owns that),
// citing coverage-so-far, the expected-by-schedule denominator, and concerns.
func TestCheckpointCadenceAndDigest(t *testing.T) {
	t.Setenv("ORION_CHECKPOINT_EVERY", "2")
	t.Setenv("ORION_CHECKPOINT_MODE", "advisory")
	cp, events := checkpointFixture(t, 8)
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		cp.taskCompleted(ctx, fmt.Sprintf("cl%d-t", i), acceptFor(fmt.Sprintf("case%d", i)))
	}
	var digests []string
	for _, e := range *events {
		if e.Phase == "Checkpoint" {
			digests = append(digests, e.Detail)
		}
	}
	if len(digests) != 3 {
		t.Fatalf("k=2 over 8 clusters → checkpoints after 2/4/6 (3 total), got %d: %v", len(digests), digests)
	}
	first := digests[0]
	for _, want := range []string{"2/8 cluster(s)", "coverage-so-far 2/8", "expected-by-schedule ~2", "alignment concerns 0"} {
		if !strings.Contains(first, want) {
			t.Fatalf("digest must cite %q, got:\n%s", want, first)
		}
	}
	// Honesty rule: no assembly-level claims mid-run.
	for _, d := range digests {
		if strings.Contains(d, "wireup") || strings.Contains(d, "orphan") {
			t.Fatalf("the mid-run digest must not claim assembly-level checks: %s", d)
		}
	}
	// Advisory never refuses dispatch.
	if err := cp.preDispatch(ctx); err != nil {
		t.Fatalf("advisory mode must never refuse dispatch: %v", err)
	}
}

// TestCheckpointPauseForAckGatesDispatch (or-v9f.26 acceptance): pause-for-ack
// refuses dispatch with a named reason until the escalation is answered, then
// resumes.
func TestCheckpointPauseForAckGatesDispatch(t *testing.T) {
	t.Setenv("ORION_CHECKPOINT_EVERY", "1")
	t.Setenv("ORION_CHECKPOINT_MODE", "pause-for-ack")
	cp, _ := checkpointFixture(t, 4)
	ctx := context.Background()

	cp.taskCompleted(ctx, "cl0-t", acceptFor("case0"))
	err := cp.preDispatch(ctx)
	if err == nil || !strings.Contains(err.Error(), "checkpoint awaiting acknowledgement") {
		t.Fatalf("pause-for-ack must refuse dispatch with a named reason: %v", err)
	}

	// Answer the escalation → dispatch resumes.
	cp.mu.Lock()
	escID := cp.pendingAck
	cp.mu.Unlock()
	if escID == "" {
		t.Fatal("the checkpoint must file an inbox escalation")
	}
	if err := cp.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.Escalations().Resolve(ctx, escID, "reviewed — trajectory acceptable, continue")
	}); err != nil {
		t.Fatal(err)
	}
	if err := cp.preDispatch(ctx); err != nil {
		t.Fatalf("an answered checkpoint must resume dispatch: %v", err)
	}
}

// TestCheckpointNotifiesWebhook (or-v9f.26 acceptance): the checkpoint event
// reaches the webhook with kind=checkpoint and a non-empty digest.
func TestCheckpointNotifiesWebhook(t *testing.T) {
	var mu sync.Mutex
	var got []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e map[string]string
		_ = json.NewDecoder(r.Body).Decode(&e)
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("ORION_NOTIFY_WEBHOOK", srv.URL)
	t.Setenv("ORION_CHECKPOINT_EVERY", "1")
	t.Setenv("ORION_CHECKPOINT_MODE", "advisory")

	cp, _ := checkpointFixture(t, 4)
	cp.taskCompleted(context.Background(), "cl0-t", acceptFor("case0"))

	mu.Lock()
	defer mu.Unlock()
	var seen bool
	for _, e := range got {
		if e["kind"] == "checkpoint" && strings.Contains(e["detail"], "trajectory after") {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("the webhook must receive kind=checkpoint with the digest, got %v", got)
	}
}
