// Package tracker projects the Epic/Task subset of the Context Store out to a
// task tracker (or-cte, PRD Core Data Model Hardening / tracker). The projection
// is ONE-WAY (store → tracker): the Context Store is the source of truth and the
// tracker is a view. V2.0 ships the beads backend as a JSONL projection (beads'
// passive-export format); a tracker_sync record tracks the last projection.
//
// Manifesto: the Context Store is truth; the tracker is a projection.
package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// Task is the projected task subset.
type Task struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// Backend is a one-way projection target.
type Backend interface {
	Name() string
	Project(ctx context.Context, epicTitle string, tasks []Task) error
}

// JSONLBackend writes the task subset as JSON Lines (the beads passive-export
// shape). One-way only — it never reads back into the store.
type JSONLBackend struct{ Path string }

func (b JSONLBackend) Name() string { return "beads" }

func (b JSONLBackend) Project(_ context.Context, epicTitle string, tasks []Task) error {
	var sb strings.Builder
	for _, t := range tasks {
		line, err := json.Marshal(map[string]string{"id": t.ID, "title": t.Title, "status": t.Status, "epic": epicTitle})
		if err != nil {
			return err
		}
		sb.Write(line)
		sb.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(b.Path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(b.Path, []byte(sb.String()), 0o644)
}

// Projector reads the store and projects to a backend.
type Projector struct {
	store   *contextstore.Store
	backend Backend
}

// New selects a tracker backend by name (tracker.backend). "beads" (default)
// projects to <dir>/tracker.jsonl.
func New(backend string, store *contextstore.Store, dir string) (*Projector, error) {
	switch backend {
	case "", "beads":
		return &Projector{store: store, backend: JSONLBackend{Path: filepath.Join(dir, "tracker.jsonl")}}, nil
	default:
		return nil, fmt.Errorf("tracker: unknown backend %q (V2.0 supports beads)", backend)
	}
}

// Backend returns the configured backend (for assertions/inspection).
func (p *Projector) Backend() Backend { return p.backend }

// Project writes the project's epic + tasks to the tracker and records a
// tracker_sync entry. It never mutates task state in the store.
func (p *Projector) Project(ctx context.Context, projectID string) (int, error) {
	var epicTitle string
	var tasks []Task
	if err := p.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		epic, err := tx.Epics().LatestForProject(ctx, projectID)
		if err != nil {
			return err
		}
		epicTitle = epic.Title
		ts, err := tx.Tasks().ListByEpic(ctx, epic.ID)
		if err != nil {
			return err
		}
		for _, t := range ts {
			tasks = append(tasks, Task{ID: t.ID, Title: t.Title, Status: t.Status})
		}
		return nil
	}); err != nil {
		return 0, err
	}
	if err := p.backend.Project(ctx, epicTitle, tasks); err != nil {
		return 0, err
	}
	// tracker_sync record (store→tracker only; conflict policy = store-wins).
	sync, _ := json.Marshal(map[string]any{"backend": p.backend.Name(), "task_count": len(tasks), "conflict_policy": "store-wins-with-alert"})
	_ = p.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.PolarisContext().Upsert(ctx, projectID, "tracker_sync", string(sync), 0)
	})
	return len(tasks), nil
}
