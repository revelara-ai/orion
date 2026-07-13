package contextstore

import (
	"context"
	"encoding/json"
)

// Inter-module interface manifests (or-7et.5). Two deterministic, task-keyed
// records — never heat-ranked recall:
//   - ModuleRequiresKind+taskID: the plan's DECLARED Requires (advisory,
//     written at plan time from the proposer's manifest).
//   - ModuleSurfaceKind+taskID: the EXTRACTED exported surface of the proven
//     artifact (the trust-wall truth, written on proof Accept).
const (
	ModuleSurfaceKind  = "module_surface:"
	ModuleRequiresKind = "module_requires:"
	// ObservedScopeKind+taskID (or-tcs.11): the file paths a proven module
	// ACTUALLY wrote — integration leases prefer this over the declaration.
	ObservedScopeKind = "observed_scope:"
)

// SaveStringListKind persists a JSON string list under projectID+kind.
func (s *Store) SaveStringListKind(ctx context.Context, projectID, kind string, list []string) error {
	if len(list) == 0 {
		return nil
	}
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return s.WithTx(ctx, func(tx *Tx) error {
		return tx.PolarisContext().Upsert(ctx, projectID, kind, string(b), 0)
	})
}

// LoadStringListKind reads a JSON string list persisted under projectID+kind.
func (s *Store) LoadStringListKind(ctx context.Context, projectID, kind string) ([]string, bool, error) {
	var payload string
	err := s.WithTx(ctx, func(tx *Tx) error {
		e, ok, err := tx.PolarisContext().Get(ctx, projectID, kind)
		if err == nil && ok {
			payload = e.Payload
		}
		return err
	})
	if err != nil || payload == "" {
		return nil, false, err
	}
	var out []string
	if uerr := json.Unmarshal([]byte(payload), &out); uerr != nil || len(out) == 0 {
		return nil, false, nil
	}
	return out, true, nil
}
