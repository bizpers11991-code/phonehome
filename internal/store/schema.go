package store

import (
	"context"
	"database/sql"
	"fmt"
)

// migrations are applied in order; migrations[i] upgrades schema version i to
// i+1. Never edit a released step: append a new one.
//
// Design notes for v1:
//   - dns is WITHOUT ROWID with primary key (ts, seq), so rows are stored in
//     time order and a period scan is one contiguous range read with no
//     secondary index. seq is a store-assigned tiebreaker (meta.dns_seq),
//     because lookups frequently share a timestamp.
//   - dns.domain_id and dns.source_id are deliberately not declared as
//     foreign keys: deleting a domain would then scan the whole fact table
//     unless dns(domain_id) were indexed, which would cost ~50% more disk.
//     The store is the only writer and Prune keeps them consistent.
//   - domains.last_ts is an upper bound on the newest lookup of that domain,
//     which lets Prune find orphaned domains without scanning dns.
var migrations = []string{
	`CREATE TABLE sources (
		id   INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE
	);
	CREATE TABLE domains (
		id      INTEGER PRIMARY KEY,
		name    TEXT NOT NULL UNIQUE,
		last_ts INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE dns (
		ts        INTEGER NOT NULL,
		seq       INTEGER NOT NULL,
		client    BLOB    NOT NULL,
		domain_id INTEGER NOT NULL,
		qtype     TEXT    NOT NULL,
		blocked   INTEGER NOT NULL,
		source_id INTEGER NOT NULL,
		PRIMARY KEY (ts, seq)
	) WITHOUT ROWID;
	CREATE TABLE flows (
		id          INTEGER PRIMARY KEY,
		start_ts    INTEGER NOT NULL,
		end_ts      INTEGER NOT NULL,
		client      BLOB    NOT NULL,
		remote      BLOB    NOT NULL,
		remote_port INTEGER NOT NULL,
		proto       TEXT    NOT NULL,
		bytes_out   INTEGER NOT NULL,
		bytes_in    INTEGER NOT NULL,
		source_id   INTEGER NOT NULL
	);
	CREATE INDEX flows_start ON flows(start_ts);
	CREATE TABLE devices (
		id         TEXT PRIMARY KEY,
		mac        TEXT    NOT NULL DEFAULT '',
		hostname   TEXT    NOT NULL DEFAULT '',
		vendor     TEXT    NOT NULL DEFAULT '',
		kind       TEXT    NOT NULL DEFAULT '',
		label      TEXT    NOT NULL DEFAULT '',
		first_seen INTEGER NOT NULL DEFAULT 0,
		last_seen  INTEGER NOT NULL DEFAULT 0
	) WITHOUT ROWID;
	-- An IP belongs to the device that most recently claimed it.
	CREATE TABLE device_ips (
		ip        BLOB PRIMARY KEY,
		device_id TEXT    NOT NULL REFERENCES devices(id),
		last_seen INTEGER NOT NULL
	) WITHOUT ROWID;
	CREATE INDEX device_ips_device ON device_ips(device_id);
	CREATE TABLE cursors (
		source TEXT PRIMARY KEY,
		cursor TEXT NOT NULL
	) WITHOUT ROWID;
	CREATE TABLE source_runs (
		name       TEXT PRIMARY KEY,
		kind       TEXT    NOT NULL,
		last_run   INTEGER NOT NULL,
		last_ok    INTEGER NOT NULL,
		records    INTEGER NOT NULL,
		last_error TEXT    NOT NULL
	) WITHOUT ROWID;
	INSERT INTO meta(key, value) VALUES ('dns_seq', 0), ('domains_gen', 0);`,
}

// migrate brings the schema up to len(migrations), one transaction per step.
// The version is read inside each write transaction, so two processes
// opening the same file concurrently cannot apply a step twice.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS meta (
		key   TEXT PRIMARY KEY,
		value INTEGER NOT NULL
	) WITHOUT ROWID`); err != nil {
		return fmt.Errorf("create meta: %w", err)
	}
	for done := false; !done; {
		err := inTx(ctx, s.db, func(tx *sql.Tx) error {
			var v int
			err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&v)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			switch {
			case v > len(migrations):
				return fmt.Errorf("schema version %d is newer than this build supports (%d)", v, len(migrations))
			case v == len(migrations):
				done = true
				return nil
			}
			if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
				return fmt.Errorf("to v%d: %w", v+1, err)
			}
			_, err = tx.ExecContext(ctx,
				`INSERT INTO meta(key, value) VALUES ('schema_version', ?)
				 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, v+1)
			return err
		})
		if err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}
