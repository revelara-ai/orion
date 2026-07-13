package conductor

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/notify"
	"github.com/revelara-ai/orion/internal/proof"
)

// Milestone checkpoints (or-v9f.26): on a weeks-long run every human
// touchpoint was reactive — exhaustion escalations, the 3-concern pause, the
// red button. The checkpoint is the PROACTIVE one: every k completed
// clusters (and optionally every ORION_CHECKPOINT_INTERVAL of wall clock) the
// run emits a trajectory digest — drift-so-far WITHOUT an assembly (mid-run
// there is no integrated tree): coverage-so-far across completed clusters'
// proof reports vs (a) total required and (b) expected-by-schedule, plus
// accumulated alignment concerns and open escalations. Honesty rule: the
// digest names only what it evaluated — no wireup/scope claims mid-run.
//
// ORION_CHECKPOINT_MODE=advisory (default) never blocks dispatch;
// pause-for-ack files an inbox escalation and refuses further dispatch until
// it is answered (the or-v9f.6 escalation/answer flow).

type checkpointer struct {
	mu             sync.Mutex
	store          *contextstore.Store
	stateMu        *sync.Mutex
	projID, runID  string
	onPhase        PhaseSink
	requiredIDs    []string
	totalClusters  int
	k              int
	interval       time.Duration
	lastCheckpoint time.Time
	membersLeft    map[string]int    // cluster key → members not yet completed
	taskCluster    map[string]string // task id → cluster key
	completed      int               // clusters fully completed
	checkpoints    int
	covered        map[string]bool // required obligation ids observed executed+passed
	concernCount   func() int      // the drift monitor's accumulated concerns
	pendingAck     string          // escalation id awaiting an answer (pause-for-ack)
}

func newCheckpointer(store *contextstore.Store, stateMu *sync.Mutex, projID, runID string, onPhase PhaseSink,
	requiredIDs []string, clusters map[string][]string, concernCount func() int) *checkpointer {
	cp := &checkpointer{
		store: store, stateMu: stateMu, projID: projID, runID: runID, onPhase: onPhase,
		requiredIDs: requiredIDs, covered: map[string]bool{},
		membersLeft: map[string]int{}, taskCluster: map[string]string{},
		concernCount: concernCount, lastCheckpoint: time.Now(),
	}
	for key, members := range clusters {
		cp.membersLeft[key] = len(members)
		for _, m := range members {
			cp.taskCluster[m] = key
		}
	}
	cp.totalClusters = len(clusters)
	cp.k = cp.totalClusters / 4
	if cp.k < 1 {
		cp.k = 1
	}
	if raw := strings.TrimSpace(os.Getenv("ORION_CHECKPOINT_EVERY")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			cp.k = n
		}
	}
	if raw := strings.TrimSpace(os.Getenv("ORION_CHECKPOINT_INTERVAL")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			cp.interval = d
		}
	}
	return cp
}

func checkpointMode() string {
	if os.Getenv("ORION_CHECKPOINT_MODE") == "pause-for-ack" {
		return "pause-for-ack"
	}
	return "advisory"
}

// taskCompleted feeds one finished task; when its cluster's last member
// lands, the cluster counts, and every k clusters (or the wall interval) a
// checkpoint fires.
func (cp *checkpointer) taskCompleted(ctx context.Context, taskID string, report proof.Report) {
	if cp == nil {
		return
	}
	cp.mu.Lock()
	for id, res := range report.ObligationResults {
		if res.Executed && res.Passed {
			cp.covered[id] = true
		}
	}
	clusterDone := false
	if key, ok := cp.taskCluster[taskID]; ok {
		cp.membersLeft[key]--
		if cp.membersLeft[key] == 0 {
			cp.completed++
			clusterDone = true
		}
	}
	due := (clusterDone && cp.completed%cp.k == 0 && cp.completed < cp.totalClusters) ||
		(cp.interval > 0 && time.Since(cp.lastCheckpoint) >= cp.interval)
	if !due {
		cp.mu.Unlock()
		return
	}
	cp.lastCheckpoint = time.Now()
	cp.checkpoints++
	n := cp.checkpoints
	digest := cp.digestLocked()
	cp.mu.Unlock()

	cp.emit(ctx, n, digest)
}

// digestLocked renders the drift-so-far trajectory digest. Caller holds cp.mu.
func (cp *checkpointer) digestLocked() string {
	required := len(cp.requiredIDs)
	coveredRequired := 0
	for _, id := range cp.requiredIDs {
		if cp.covered[id] {
			coveredRequired++
		}
	}
	expected := 0
	if cp.totalClusters > 0 {
		expected = required * cp.completed / cp.totalClusters
	}
	concerns := 0
	if cp.concernCount != nil {
		concerns = cp.concernCount()
	}
	openEsc := cp.openEscalations()
	return fmt.Sprintf(
		"trajectory after %d/%d cluster(s): coverage-so-far %d/%d required obligations (expected-by-schedule ~%d); alignment concerns %d; open escalations %d. Evaluated: obligation coverage, alignment concerns, escalations — assembly-level checks run at delivery.",
		cp.completed, cp.totalClusters, coveredRequired, required, expected, concerns, openEsc)
}

func (cp *checkpointer) openEscalations() int {
	n := 0
	withLock(cp.stateMu, func() {
		_ = cp.store.WithTx(context.Background(), func(tx *contextstore.Tx) error {
			open, err := tx.Escalations().ListOpen(context.Background())
			if err == nil {
				n = len(open)
			}
			return nil
		})
	})
	return n
}

// emit persists + surfaces the checkpoint; pause-for-ack files the inbox
// escalation the dispatch gate then waits on. NEVER called under cp.mu or a
// store tx (the teed phase sink writes to the store).
func (cp *checkpointer) emit(ctx context.Context, n int, digest string) {
	status := PhaseDone
	if strings.Contains(digest, "concerns 0; open escalations 0") == false {
		status = PhaseWarn
	}
	withLock(cp.stateMu, func() {
		_ = cp.store.SaveStringListKind(ctx, cp.projID, fmt.Sprintf("checkpoint:%s:%d", cp.runID, n), []string{digest})
	})
	cp.onPhase.emit("Checkpoint", status, digest)
	next := "review with: orion trace"
	if checkpointMode() == "pause-for-ack" {
		var escID string
		withLock(cp.stateMu, func() {
			_ = cp.store.WithTx(ctx, func(tx *contextstore.Tx) error {
				id, e := tx.Escalations().CreateDetailed(ctx, cp.projID, "",
					fmt.Sprintf("checkpoint %d review (pause-for-ack)", n), digest)
				if e == nil {
					escID = id
				}
				return e
			})
		})
		if escID != "" {
			cp.mu.Lock()
			cp.pendingAck = escID
			cp.mu.Unlock()
			next = "acknowledge with: orion escalations resolve " + escID
		}
	}
	_ = notify.Notify(ctx, notify.Event{Kind: "checkpoint", Detail: digest, NextAction: next})
}

// preDispatch composes into the dispatch gate: advisory never refuses;
// pause-for-ack refuses while the checkpoint escalation is unanswered.
func (cp *checkpointer) preDispatch(ctx context.Context) error {
	if cp == nil {
		return nil
	}
	cp.mu.Lock()
	pending := cp.pendingAck
	cp.mu.Unlock()
	if pending == "" {
		return nil
	}
	stillOpen := false
	withLock(cp.stateMu, func() {
		_ = cp.store.WithTx(ctx, func(tx *contextstore.Tx) error {
			open, err := tx.Escalations().ListOpen(ctx)
			if err != nil {
				return nil
			}
			for _, e := range open {
				if e.ID == pending {
					stillOpen = true
				}
			}
			return nil
		})
	})
	if stillOpen {
		return fmt.Errorf("checkpoint awaiting acknowledgement — answer it to resume dispatch: orion escalations resolve %s", pending)
	}
	cp.mu.Lock()
	cp.pendingAck = ""
	cp.mu.Unlock()
	return nil
}
