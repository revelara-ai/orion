package conductor

import (
	"context"
	"encoding/json"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/harness"
	"github.com/revelara-ai/orion/pkg/llm"
)

// storeTurnCheckpoint (or-mvr.8) backs the harness's provider-outage turn
// checkpoint with the context store, keyed per generation site (the cluster
// worktree). The conversation is GENERATION-DOMAIN data: it never enters a
// proof prompt; it only reseeds the generator's own next turn. Best-effort
// throughout — a checkpoint miss must never fail (or wedge) a build.
type storeTurnCheckpoint struct {
	store *contextstore.Store
}

const turnCheckpointKind = "turn_checkpoint:"

var _ harness.TurnCheckpoint = storeTurnCheckpoint{}

func (s storeTurnCheckpoint) projectID(ctx context.Context) string {
	proj, _, err := s.store.CurrentProjectSpec(ctx)
	if err != nil {
		return ""
	}
	return proj.ID
}

func (s storeTurnCheckpoint) Load(ctx context.Context, key string) ([]llm.Message, bool) {
	pid := s.projectID(ctx)
	if pid == "" {
		return nil, false
	}
	var payload string
	_ = s.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		e, ok, err := tx.PolarisContext().Get(ctx, pid, turnCheckpointKind+key)
		if err == nil && ok {
			payload = e.Payload
		}
		return nil
	})
	if payload == "" {
		return nil, false
	}
	var convo []llm.Message
	if err := json.Unmarshal([]byte(payload), &convo); err != nil || len(convo) == 0 {
		return nil, false
	}
	return convo, true
}

func (s storeTurnCheckpoint) Save(ctx context.Context, key string, convo []llm.Message) {
	pid := s.projectID(ctx)
	if pid == "" {
		return
	}
	b, err := json.Marshal(convo)
	if err != nil {
		return
	}
	_ = s.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.PolarisContext().Upsert(ctx, pid, turnCheckpointKind+key, string(b), 0)
	})
}

// Clear empties the payload (the store has no delete for this kind; Load
// treats an empty payload as absent).
func (s storeTurnCheckpoint) Clear(ctx context.Context, key string) {
	pid := s.projectID(ctx)
	if pid == "" {
		return
	}
	_ = s.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.PolarisContext().Upsert(ctx, pid, turnCheckpointKind+key, "", 0)
	})
}
