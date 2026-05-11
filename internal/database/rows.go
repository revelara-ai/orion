package database

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// rowsTxCloser wraps pgx.Rows so the iterator's Close() rolls back
// the surrounding tx (read-only path). RLSPool.Query uses this for
// multi-row SELECT iteration where commit semantics aren't needed.
//
// For DML (INSERT/UPDATE/DELETE ... RETURNING) the QueryRow path
// commits explicitly via singleRow.Scan; see below.
type rowsTxCloser struct {
	pgx.Rows
	tx     pgx.Tx
	ctx    context.Context //nolint:containedctx // tx commit/rollback needs the original ctx
	closed bool
}

// Close rolls back the underlying tx after closing the row iterator.
// SET LOCAL is tx-scoped so rollback is correct here for read-only
// queries (the SETs only applied for the duration of the read).
func (r *rowsTxCloser) Close() {
	if r.closed {
		return
	}
	r.closed = true
	r.Rows.Close()
	_ = r.tx.Rollback(r.ctx)
}

// commit commits the underlying tx. Used by singleRow.Scan to
// persist INSERT/UPDATE/DELETE ... RETURNING side-effects.
func (r *rowsTxCloser) commit() error {
	if r.closed {
		return nil
	}
	r.closed = true
	r.Rows.Close()
	return r.tx.Commit(r.ctx)
}

// errRow is a pgx.Row that returns the same error on every Scan.
// Used to surface RLSPool.Query failures through the QueryRow API
// without requiring callers to handle a separate error return.
type errRow struct {
	err error
}

func (e errRow) Scan(_ ...any) error {
	return e.err
}

// singleRow exposes the first row of a Rows result as a pgx.Row,
// and commits the surrounding tx on successful Scan so DML
// side-effects (INSERT ... RETURNING) persist. On error or no-row,
// the tx rolls back via Close().
type singleRow struct {
	rows *rowsTxCloser
}

func (s *singleRow) Scan(dest ...any) error {
	if !s.rows.Next() {
		if err := s.rows.Err(); err != nil {
			s.rows.Close()
			return err
		}
		s.rows.Close()
		return errors.New("database: no rows in result")
	}
	if err := s.rows.Scan(dest...); err != nil {
		s.rows.Close()
		return err
	}
	// Scan succeeded — commit so DML side-effects persist.
	return s.rows.commit()
}
