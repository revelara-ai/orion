package conductor

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/harnessconfig"
	"github.com/revelara-ai/orion/internal/llmsetup"
	"github.com/revelara-ai/orion/pkg/llm"
)

// Per-role model routing (or-kzf.4): frontier-for-hard, cheap-for-easy is a
// financial strategy — generation may need the big brain while review/grill/
// distill ride a cheaper one. Resolution precedence per role:
//
//	1. ORION_MODEL_<ROLE> (quick env override, e.g. ORION_MODEL_REVIEW)
//	2. the reviewable harness config models.yaml roles map (or-kzf.2:
//	   versioned, diffable, canary-able)
//	3. the session brain (the commodity default — routing stays OPTIONAL)
//
// An unbuildable ref warns and falls back — a routing typo must never turn a
// role off. Every effective re-route is RECORDED (slog + the project's
// model_routing record) so a run's brains are auditable after the fact.

// RoleProvider resolves the provider a conductor role runs on.
func RoleProvider(role string, fallback llm.Provider) llm.Provider {
	ref := roleRef(role)
	if ref == "" {
		return fallback
	}
	prov, full, err := llmsetup.Rebuild(llmsetup.Select(), ref)
	if err != nil {
		slog.Warn("model routing: ref unbuildable — the session brain serves this role", "role", role, "ref", ref, "err", err)
		return fallback
	}
	slog.Info("model routing", "role", role, "model", full)
	recordRouting(role, full)
	return prov
}

// roleRef resolves the configured ref for a role (env wins over the file).
func roleRef(role string) string {
	if ref := strings.TrimSpace(os.Getenv("ORION_MODEL_" + strings.ToUpper(role))); ref != "" {
		return ref
	}
	return harnessconfig.RoleModel(role)
}

// routingLog accumulates the session's effective re-routes; flushed into the
// store when a project context is available (recordRoutingToStore).
var (
	routingMu  sync.Mutex
	routingLog = map[string]string{}
)

func recordRouting(role, ref string) {
	routingMu.Lock()
	routingLog[role] = ref
	routingMu.Unlock()
}

// RecordRoutingToStore persists the session's effective routing decisions on
// the project (kind model_routing) — the after-the-fact audit record.
func RecordRoutingToStore(ctx context.Context, store *contextstore.Store) {
	if store == nil {
		return
	}
	routingMu.Lock()
	snapshot := make(map[string]string, len(routingLog))
	for k, v := range routingLog {
		snapshot[k] = v
	}
	routingMu.Unlock()
	if len(snapshot) == 0 {
		return
	}
	proj, _, err := store.CurrentProjectSpec(ctx)
	if err != nil {
		return
	}
	b, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.PolarisContext().Upsert(ctx, proj.ID, "model_routing", string(b), 0)
	})
}
