package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// Cursor returns the saved cursor for source, or "" if there is none.
func (s *Store) Cursor(ctx context.Context, source string) (string, error) {
	var c string
	err := s.db.QueryRowContext(ctx, `SELECT cursor FROM cursors WHERE source = ?`, source).Scan(&c)
	if err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("store: cursor %s: %w", source, err)
	}
	return c, nil
}

// SetCursor saves the cursor for source.
func (s *Store) SetCursor(ctx context.Context, source, cursor string) error {
	err := s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO cursors(source, cursor) VALUES (?, ?)
			ON CONFLICT(source) DO UPDATE SET cursor = excluded.cursor`, source, cursor)
		return err
	})
	if err != nil {
		return fmt.Errorf("store: set cursor %s: %w", source, err)
	}
	return nil
}

// RecordRun records one ingestion run of a source, upserting by Name.
// st.Records is added to the running total, except for device sources (Kind
// "devices"): their runs re-report the same devices, so it is replaced.
// LastRun and LastError are overwritten; LastOK is overwritten only by a
// successful run (LastError == "") that sets it. Kind is overwritten when
// non-empty.
func (s *Store) RecordRun(ctx context.Context, st model.SourceStatus) error {
	if st.Name == "" {
		return errors.New("store: record run: empty source name")
	}
	lastOK := nanos(st.LastOK)
	if st.LastError != "" {
		lastOK = 0
	}
	err := s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO source_runs(name, kind, last_run, last_ok, records, last_error)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(name) DO UPDATE SET
				kind       = CASE WHEN excluded.kind <> '' THEN excluded.kind ELSE kind END,
				last_run   = excluded.last_run,
				last_ok    = CASE WHEN excluded.last_ok <> 0 THEN excluded.last_ok ELSE last_ok END,
				-- DNS and flow sources report new rows; device sources report
				-- the devices they currently see, so their count is replaced.
				records    = CASE WHEN excluded.kind = 'devices' THEN excluded.records
				                  ELSE records + excluded.records END,
				last_error = excluded.last_error`,
			st.Name, st.Kind, nanos(st.LastRun), lastOK, st.Records, st.LastError)
		return err
	})
	if err != nil {
		return fmt.Errorf("store: record run %s: %w", st.Name, err)
	}
	return nil
}

// Status reports source health, the device count, the time span of stored
// DNS lookups and DBPath. Version and Demo are left for the caller.
func (s *Store) Status(ctx context.Context) (model.Status, error) {
	st := model.Status{DBPath: s.path}
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, kind, last_run, last_ok, records, last_error FROM source_runs ORDER BY name`)
	if err != nil {
		return st, fmt.Errorf("store: status: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			ss          model.SourceStatus
			lastRun, ok int64
		)
		if err := rows.Scan(&ss.Name, &ss.Kind, &lastRun, &ok, &ss.Records, &ss.LastError); err != nil {
			return st, fmt.Errorf("store: status: %w", err)
		}
		ss.LastRun, ss.LastOK = fromNanos(lastRun), fromNanos(ok)
		st.Sources = append(st.Sources, ss)
	}
	if err := rows.Err(); err != nil {
		return st, fmt.Errorf("store: status: %w", err)
	}

	// Separate subqueries so SQLite answers min and max from the ends of the
	// primary key instead of scanning.
	var oldest, newest sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM devices),
		(SELECT min(ts) FROM dns),
		(SELECT max(ts) FROM dns)`).Scan(&st.Devices, &oldest, &newest); err != nil {
		return st, fmt.Errorf("store: status: %w", err)
	}
	st.Oldest, st.Newest = fromNanos(oldest.Int64), fromNanos(newest.Int64)
	return st, nil
}
