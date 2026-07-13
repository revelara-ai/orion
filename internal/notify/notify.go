// Package notify delivers out-of-band notifications about Orion runs (completion,
// escalation) to an operator — the "3 a.m. test" applied to Orion itself: a long
// autonomous run should be able to reach you when it finishes or needs a human (or-ykz.18).
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// Event is a run event worth surfacing out-of-band. The optional fields make the
// notification ACTIONABLE at 3 a.m. (or-v9f.17): the id to answer, the PR to
// review, the exact command to run — not just "something happened".
type Event struct {
	Kind         string `json:"kind"`    // delivered | partial | escalated | escalation.created | change.delivered | change.escalated
	Task         string `json:"task"`    // task id
	Verdict      string `json:"verdict"` // converged proof verdict
	Detail       string `json:"detail"`  // delivery decision / escalation reason
	EscalationID string `json:"escalation_id,omitempty"`
	PRURL        string `json:"pr_url,omitempty"`
	Artifact     string `json:"artifact,omitempty"`    // PR artifact path, output dir, or review branch
	NextAction   string `json:"next_action,omitempty"` // the command that acts on this event
}

// Notify delivers e to the configured channel: if $ORION_NOTIFY_WEBHOOK is set it POSTs e as
// JSON there; otherwise it emits a structured log line. The webhook URL is read from the
// COORDINATOR environment (control plane) — never from inside a sandbox, so a generated
// program cannot redirect notifications. Best-effort: a delivery failure is logged and
// returned, but callers wire it as fire-and-forget so it never fails a run.
func Notify(ctx context.Context, e Event) error {
	return notify(ctx, e, os.Getenv("ORION_NOTIFY_WEBHOOK"), http.DefaultClient)
}

// notify is Notify with the webhook + client injected, for tests.
func notify(ctx context.Context, e Event, webhook string, client *http.Client) error {
	if webhook == "" {
		slog.Info("orion run notification",
			"kind", e.Kind, "task", e.Task, "verdict", e.Verdict, "detail", e.Detail)
		return nil
	}
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, webhook, bytes.NewReader(body)) // #nosec G704 -- the operator configures their own webhook URL (ORION_NOTIFY_WEBHOOK)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req) // #nosec G704 -- operator-configured webhook (see above)
	if err != nil {
		slog.Warn("orion notification delivery failed", "kind", e.Kind, "err", err)
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("notify webhook: status %d", resp.StatusCode)
	}
	return nil
}
