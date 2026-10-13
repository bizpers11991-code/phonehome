package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// pruneChunk is how many rows Prune deletes per transaction. Small enough
// that ingestion on a Pi waits only briefly for the write lock between chunks.
const pruneChunk = 5000

// Prune deletes DNS lookups and flows older than before, then the domains no
// remaining lookup refers to. It works in short transactions and returns the
// number of lookups and flows deleted.
func (s *Store) Prune(ctx context.Context, before time.Time) (int64, error) {
	cut := nanos(before)
	var total int64
	for _, q := range []string{
		`DELETE FROM dns WHERE (ts, seq) IN (SELECT ts, seq FROM dns WHERE ts < ?1 LIMIT ?2)`,
		`DELETE FROM flows WHERE id IN (SELECT id FROM flows WHERE start_ts < ?1 LIMIT ?2)`,
	} {
		n, err := s.deleteChunks(ctx, cut, q, nil)
		total += n
		if err != nil {
			return total, fmt.Errorf("store: prune: %w", err)
		}
	}

	// A domain's lookups are all at or before its last_ts, so if last_ts is
	// before the cut, every lookup is in the range just pruned. The NOT EXISTS
	// re-checks that (now nearly empty) range for rows written concurrently.
	_, err := s.deleteChunks(ctx, cut, `
		DELETE FROM domains WHERE id IN (
			SELECT id FROM domains
			WHERE last_ts < ?1 AND NOT EXISTS (SELECT 1 FROM dns WHERE ts < ?1 AND domain_id = domains.id)
			LIMIT ?2)`,
		func(tx *sql.Tx) error {
			// Invalidate every process's domain cache: ids may be reused.
			_, err := tx.ExecContext(ctx, `UPDATE meta SET value = value + 1 WHERE key = 'domains_gen'`)
			return err
		})
	if err != nil {
		return total, fmt.Errorf("store: prune domains: %w", err)
	}
	if err := s.reclaim(ctx); err != nil {
		return total, fmt.Errorf("store: reclaim space: %w", err)
	}
	return total, nil
}

// vacuumStep is how many free pages one incremental_vacuum transaction
// releases, so that ingestion waits only briefly for the write lock.
const vacuumStep = 2000

// minReclaim is the number of free pages below which the file is left as it
// is: they will be reused by the next inserts.
const minReclaim = 256

// AutoVacuum reports whether the database was created with incremental
// auto-vacuum, so that pruning can shrink the file. Databases made by
// earlier versions were not, and only a full VACUUM (with phonehome
// stopped) can change that.
func (s *Store) AutoVacuum() bool {
	var mode int
	return s.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode) == nil && mode == 2
}

// reclaim returns free pages to the file system after a prune freed a large
// share of the file (at least a quarter and minReclaim pages), in short
// incremental_vacuum steps, then truncates the WAL so that the file shrinks
// now rather than at some later checkpoint. It does nothing for a database
// without incremental auto-vacuum.
func (s *Store) reclaim(ctx context.Context) error {
	if !s.AutoVacuum() {
		return nil
	}
	var free, pages int64
	if err := s.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err != nil {
		return err
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return err
	}
	if free < minReclaim || free*4 < pages {
		return nil
	}
	for ; free > 0; free -= vacuumStep {
		if err := s.write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, vacuumStep))
			return err
		}); err != nil {
			return err
		}
	}
	// TRUNCATE waits for readers; if one is busy the checkpoint just
	// happens later, so its failure is not an error.
	s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return nil
}

// deleteChunks runs the chunked DELETE q (parameters ?1 = cut, ?2 = chunk
// size) until it deletes nothing, one transaction per chunk. after, if not
// nil, runs in each transaction that deleted rows.
func (s *Store) deleteChunks(ctx context.Context, cut int64, q string, after func(*sql.Tx) error) (int64, error) {
	var total int64
	for {
		var n int64
		err := s.write(ctx, func(tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, q, cut, pruneChunk)
			if err != nil {
				return err
			}
			if n, err = res.RowsAffected(); err != nil || n == 0 || after == nil {
				return err
			}
			return after(tx)
		})
		if err != nil {
			return total, err
		}
		total += n
		if n < pruneChunk {
			return total, nil
		}
	}
}
