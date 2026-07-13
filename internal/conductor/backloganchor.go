package conductor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// Backlog snapshot hash-anchoring (or-v9f.7): issues the brain CONSUMES while
// grounding a spec are anchored like the spec itself — a content hash taken at
// FIRST read (the intake snapshot; later re-reads never move it). At system
// validation the anchors are re-derived from the live tracker: an external
// mid-run edit surfaces as an inbox escalation — a spec-amendment decision for
// the developer — instead of silently informing nothing. Only CONTENT fields
// are hashed (title, description, labels, priority, type): the run's own
// lifecycle writes (claim, close) are not drift.

const backlogAnchorKind = "backlog_anchor"

// backlogIssueHash canonicalizes one issue from `bd show <id> --json` output
// into a content hash. ok=false when the issue is unreadable/unparsable.
func backlogIssueHash(ctx context.Context, root, id string) (string, bool) {
	out, exit := bdRun(ctx, root, "show", id, "--json")
	if exit != 0 {
		return "", false
	}
	var issues []map[string]any
	if err := json.Unmarshal([]byte(out), &issues); err != nil || len(issues) == 0 {
		return "", false
	}
	iss := issues[0]
	canon := map[string]any{}
	for _, k := range []string{"title", "description", "labels", "priority", "issue_type"} {
		canon[k] = iss[k]
	}
	b, err := json.Marshal(canon) // map keys marshal sorted — deterministic
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), true
}

// loadBacklogAnchor reads the project's anchor map (issue id → content hash).
func loadBacklogAnchor(ctx context.Context, store *contextstore.Store, projectID string) map[string]string {
	anchors := map[string]string{}
	_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
		e, ok, err := tx.PolarisContext().Get(ctx, projectID, backlogAnchorKind)
		if err != nil || !ok {
			return nil
		}
		_ = json.Unmarshal([]byte(e.Payload), &anchors)
		return nil
	})
	return anchors
}

// recordBacklogAnchor anchors one consumed issue: FIRST read wins — a re-read
// after an external edit must not quietly move the anchor onto the edit (that
// would mask exactly the drift this exists to surface).
func recordBacklogAnchor(ctx context.Context, store *contextstore.Store, projectID, root, id string) {
	if projectID == "" || id == "" {
		return
	}
	// Hash OUTSIDE the tx: bd is an exec, and the store is single-connection.
	hash, ok := backlogIssueHash(ctx, root, id)
	if !ok {
		return
	}
	_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
		anchors := map[string]string{}
		if e, found, err := tx.PolarisContext().Get(ctx, projectID, backlogAnchorKind); err == nil && found {
			_ = json.Unmarshal([]byte(e.Payload), &anchors)
		}
		if _, already := anchors[id]; already {
			return nil // intake snapshot is immutable
		}
		anchors[id] = hash
		b, err := json.Marshal(anchors)
		if err != nil {
			return nil
		}
		return tx.PolarisContext().Upsert(ctx, projectID, backlogAnchorKind, string(b), 0)
	})
}

// backlogDriftCheck re-derives every anchored issue's content hash from the
// live tracker and reports mismatches. drifted=false when nothing was anchored
// or everything still matches.
func backlogDriftCheck(ctx context.Context, store *contextstore.Store, projectID, root string) (string, bool) {
	anchors := loadBacklogAnchor(ctx, store, projectID)
	if len(anchors) == 0 {
		return "", false
	}
	ids := make([]string, 0, len(anchors))
	for id := range anchors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var drifted []string
	for _, id := range ids {
		now, ok := backlogIssueHash(ctx, root, id)
		switch {
		case !ok:
			drifted = append(drifted, id+" (no longer readable)")
		case now != anchors[id]:
			drifted = append(drifted, id+" (content edited since intake)")
		}
	}
	if len(drifted) == 0 {
		return fmt.Sprintf("backlog anchors verified: %d issue(s) unchanged since intake", len(ids)), false
	}
	return "consumed backlog issues changed mid-run: " + strings.Join(drifted, ", ") +
		" — the spec was grounded in the intake snapshot; review whether the amendment invalidates it", true
}
