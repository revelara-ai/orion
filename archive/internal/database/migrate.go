package database

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate applies any pending up-migrations from migrations/*.up.sql
// in lexical order, against the raw pool (system-level operation;
// not RLS-scoped). Tracks applied migrations in schema_migrations.
// Idempotent: re-running is a no-op after the last migration applies.
//
// File naming: NNN_description.up.sql. Lexical sort + zero-padded
// numbers means lexical order == numeric order through 999
// migrations.
func Migrate(ctx context.Context, pool *Pool) error {
	if err := ensureMigrationsTable(ctx, pool); err != nil {
		return err
	}
	applied, err := loadAppliedMigrations(ctx, pool)
	if err != nil {
		return err
	}
	files, err := listUpMigrations()
	if err != nil {
		return err
	}
	for _, f := range files {
		if applied[f] {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + f)
		if err != nil {
			return fmt.Errorf("database: read migration %s: %w", f, err)
		}
		if err := applyMigration(ctx, pool, f, string(body)); err != nil {
			return fmt.Errorf("database: apply %s: %w", f, err)
		}
	}
	return nil
}

func ensureMigrationsTable(ctx context.Context, pool *Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name        text PRIMARY KEY,
			applied_at  timestamptz NOT NULL DEFAULT now()
		)
	`)
	if err != nil {
		return fmt.Errorf("database: create schema_migrations: %w", err)
	}
	return nil
}

func loadAppliedMigrations(ctx context.Context, pool *Pool) (map[string]bool, error) {
	rows, err := pool.Query(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("database: list applied: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func listUpMigrations() ([]string, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("database: list migrations: %w", err)
	}
	var ups []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		ups = append(ups, e.Name())
	}
	sort.Strings(ups)
	return ups, nil
}

func applyMigration(ctx context.Context, pool *Pool, name, body string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // rollback after commit is no-op
	if _, err := tx.Exec(ctx, body); err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, name); err != nil {
		return fmt.Errorf("record: %w", err)
	}
	return tx.Commit(ctx)
}
