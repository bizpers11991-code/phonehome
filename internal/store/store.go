// Package store persists phonehome's observations in a single SQLite file
// using the pure-Go modernc.org/sqlite driver.
//
// The schema is tuned for a Raspberry Pi on an SD card holding ~90 days of a
// busy home: DNS lookups live in a WITHOUT ROWID table clustered on time, with
// domain names and source names normalised into lookup tables and client IPs
// stored as 4- or 16-byte blobs. See schema.go for the tables.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// pragmas are applied to every connection. busy_timeout covers other
// processes (e.g. `phonehome ingest --once` next to `phonehome serve`);
// _txlock=immediate makes write transactions take the write lock up front so
// they wait on busy_timeout instead of failing on a read→write upgrade.
const pragmas = "_pragma=busy_timeout(10000)" +
	"&_pragma=journal_mode(WAL)" +
	"&_pragma=synchronous(NORMAL)" +
	"&_pragma=foreign_keys(1)" +
	"&_txlock=immediate"

// maxReaders bounds the connection pool for file databases. WAL lets readers
// run concurrently with the single writer; a Pi has four cores.
const maxReaders = 4

// Store is a handle to the phonehome database. It is safe for concurrent use.
type Store struct {
	db   *sql.DB
	path string

	// wmu serialises writers inside this process, so they never contend on
	// SQLite's lock, and guards the lookup caches below.
	wmu       sync.Mutex
	domains   domainCache
	sources   map[string]int64
	domainGen int64 // meta.domains_gen the domain cache was filled under
}

// Open opens (creating if needed) the database at path and migrates it to
// the current schema. path ":memory:" gives a private in-memory database.
func Open(path string) (*Store, error) {
	dsn, dbPath := ":memory:?"+pragmas, path
	if path != ":memory:" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("store: open %s: %w", path, err)
		}
		dbPath = abs
		dsn = "file:" + (&url.URL{Path: abs}).EscapedPath() + "?" + pragmas
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	if path == ":memory:" {
		// Every connection to :memory: is a separate database, so keep
		// exactly one and never let it be closed while idle.
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(maxReaders)
	}
	db.SetMaxIdleConns(maxReaders)

	s := &Store{
		db:      db,
		path:    dbPath,
		domains: domainCache{max: domainCacheSize},
		sources: make(map[string]int64),
	}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	return s, nil
}

// Close updates the query planner's statistics and closes the database.
func (s *Store) Close() error {
	_, optErr := s.db.Exec("PRAGMA optimize")
	if err := errors.Join(optErr, s.db.Close()); err != nil {
		return fmt.Errorf("store: close: %w", err)
	}
	return nil
}

// write runs fn in a write transaction, holding the writer lock.
func (s *Store) write(ctx context.Context, fn func(*sql.Tx) error) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return inTx(ctx, s.db, fn)
}

// inTx runs fn in a transaction, committing if it returns nil.
func inTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	return tx.Commit()
}

// nanos encodes t as unix nanoseconds, mapping the zero time to 0.
func nanos(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

// fromNanos is the inverse of nanos. Times come back in UTC.
func fromNanos(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

// ipBytes encodes a as 4 bytes (IPv4) or 16 bytes (IPv6), half the size of
// the text form for the common IPv4 case and cheaper to compare. The zero
// Addr encodes as an empty blob; zones are dropped.
func ipBytes(a netip.Addr) []byte {
	b := a.AsSlice()
	if b == nil {
		return []byte{}
	}
	return b
}

// ipFrom is the inverse of ipBytes.
func ipFrom(b []byte) netip.Addr {
	a, _ := netip.AddrFromSlice(b)
	return a
}
