package stpa

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// stpaKind is the Polaris-domain key under which the ratified model is persisted.
// V2.0 persists the developer-ratified model locally (the trusted source for
// hazard proof); the Polaris write-back (control-structure/ucas/loss-scenarios)
// is a thin adapter over this when the connector's write path lands.
const stpaKind = "stpa_model"

// Save persists the ratified STPA model for a project (retrievable for proof).
func Save(ctx context.Context, store *contextstore.Store, projectID string, m Model) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return store.WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.PolarisContext().Upsert(ctx, projectID, stpaKind, string(b), 0)
	})
}

// Load retrieves the ratified STPA model for a project.
func Load(ctx context.Context, store *contextstore.Store, projectID string) (Model, bool, error) {
	var m Model
	var found bool
	err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
		e, ok, err := tx.PolarisContext().Get(ctx, projectID, stpaKind)
		if err != nil || !ok {
			return err
		}
		found = true
		return json.Unmarshal([]byte(e.Payload), &m)
	})
	if err != nil {
		return Model{}, false, fmt.Errorf("stpa load: %w", err)
	}
	return m, found, nil
}
