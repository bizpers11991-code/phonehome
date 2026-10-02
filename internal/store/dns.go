package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// domainCacheSize bounds the name→id cache. A busy home sees a few thousand
// distinct domains a day, so this normally holds the whole working set.
const domainCacheSize = 1 << 16

type domainEntry struct {
	id     int64
	lastTS int64 // value of domains.last_ts known to be stored
}

// domainCache maps domain names to ids. When full it is simply emptied: the
// working set refills it within a batch or two, and no eviction bookkeeping
// runs on the hot path.
type domainCache struct {
	m   map[string]domainEntry
	max int
}

func (c *domainCache) get(name string) (domainEntry, bool) {
	e, ok := c.m[name]
	return e, ok
}

func (c *domainCache) put(name string, e domainEntry) {
	if c.m == nil || len(c.m) >= c.max {
		c.m = make(map[string]domainEntry)
	}
	c.m[name] = e
}

// lookups resolves domain and source names to ids inside one write
// transaction. New ids are staged and only reach the Store's caches through
// commit, after the transaction has committed, so a rollback cannot leave
// the caches pointing at rows that do not exist. Statements prepared on tx
// are closed by database/sql when the transaction ends.
type lookups struct {
	s  *Store
	tx *sql.Tx

	selDomain, insDomain *sql.Stmt
	domains              map[string]*stagedDomain
	sources              map[string]int64
}

type stagedDomain struct {
	domainEntry
	maxTS int64 // newest lookup of the domain in this batch
}

// newLookups must be called with s.wmu held. It checks whether Prune (in
// this or another process) deleted domains since the cache was filled.
func (s *Store) newLookups(ctx context.Context, tx *sql.Tx) (*lookups, error) {
	var gen int64
	if err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'domains_gen'`).Scan(&gen); err != nil {
		return nil, err
	}
	if gen != s.domainGen {
		s.domains.m = nil
		s.domainGen = gen
	}
	return &lookups{s: s, tx: tx, domains: make(map[string]*stagedDomain), sources: make(map[string]int64)}, nil
}

// domain returns the id for name, creating the row if needed, and records
// that name was looked up at ts.
func (l *lookups) domain(ctx context.Context, name string, ts int64) (int64, error) {
	d, ok := l.domains[name]
	if !ok {
		e, err := l.resolveDomain(ctx, name)
		if err != nil {
			return 0, err
		}
		d = &stagedDomain{domainEntry: e}
		l.domains[name] = d
	}
	d.maxTS = max(d.maxTS, ts)
	return d.id, nil
}

func (l *lookups) resolveDomain(ctx context.Context, name string) (domainEntry, error) {
	if e, ok := l.s.domains.get(name); ok {
		return e, nil
	}
	if l.selDomain == nil {
		var err error
		if l.selDomain, err = l.tx.PrepareContext(ctx, `SELECT id, last_ts FROM domains WHERE name = ?`); err != nil {
			return domainEntry{}, err
		}
		if l.insDomain, err = l.tx.PrepareContext(ctx, `INSERT INTO domains(name) VALUES (?) RETURNING id`); err != nil {
			return domainEntry{}, err
		}
	}
	var e domainEntry
	err := l.selDomain.QueryRowContext(ctx, name).Scan(&e.id, &e.lastTS)
	if err == sql.ErrNoRows {
		err = l.insDomain.QueryRowContext(ctx, name).Scan(&e.id)
	}
	return e, err
}

// source returns the id for a source name, creating the row if needed.
func (l *lookups) source(ctx context.Context, name string) (int64, error) {
	if id, ok := l.s.sources[name]; ok {
		return id, nil
	}
	if id, ok := l.sources[name]; ok {
		return id, nil
	}
	var id int64
	err := l.tx.QueryRowContext(ctx, `SELECT id FROM sources WHERE name = ?`, name).Scan(&id)
	if err == sql.ErrNoRows {
		err = l.tx.QueryRowContext(ctx, `INSERT INTO sources(name) VALUES (?) RETURNING id`, name).Scan(&id)
	}
	if err != nil {
		return 0, err
	}
	l.sources[name] = id
	return id, nil
}

// flush raises domains.last_ts for every domain this batch saw at a newer
// time than is known to be stored.
func (l *lookups) flush(ctx context.Context) error {
	var stmt *sql.Stmt
	for _, d := range l.domains {
		if d.maxTS <= d.lastTS {
			continue
		}
		if stmt == nil {
			var err error
			if stmt, err = l.tx.PrepareContext(ctx, `UPDATE domains SET last_ts = max(last_ts, ?) WHERE id = ?`); err != nil {
				return err
			}
		}
		if _, err := stmt.ExecContext(ctx, d.maxTS, d.id); err != nil {
			return err
		}
		d.lastTS = d.maxTS
	}
	return nil
}

// commit publishes staged ids to the Store's caches. Call it only after the
// transaction committed, with s.wmu still held.
func (l *lookups) commit() {
	for name, d := range l.domains {
		l.s.domains.put(name, d.domainEntry)
	}
	for name, id := range l.sources {
		l.s.sources[name] = id
	}
}

// InsertDNS stores qs in one transaction.
func (s *Store) InsertDNS(ctx context.Context, qs []model.DNSQuery) error {
	if len(qs) == 0 {
		return nil
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	var lk *lookups
	err := inTx(ctx, s.db, func(tx *sql.Tx) error {
		var err error
		if lk, err = s.newLookups(ctx, tx); err != nil {
			return err
		}
		// Reserve len(qs) tiebreakers in one statement.
		var seq int64
		if err := tx.QueryRowContext(ctx,
			`UPDATE meta SET value = value + ? WHERE key = 'dns_seq' RETURNING value`, len(qs)).Scan(&seq); err != nil {
			return err
		}
		seq -= int64(len(qs))
		// Rows go in insertRows at a time: each statement execution costs
		// far more in database/sql and the driver than a row does in SQLite.
		var full, tail *sql.Stmt
		args := make([]any, 0, insertRows*7)
		for i, q := range qs {
			ts := nanos(q.Time)
			dom, err := lk.domain(ctx, q.Domain, ts)
			if err != nil {
				return err
			}
			src, err := lk.source(ctx, q.Source)
			if err != nil {
				return err
			}
			seq++
			args = append(args, ts, seq, ipBytes(q.ClientIP), dom, q.QType, q.Blocked, src)
			if len(args) < insertRows*7 && i < len(qs)-1 {
				continue
			}
			stmt := &full
			if len(args) < insertRows*7 {
				stmt = &tail // the last, short chunk
			}
			if *stmt == nil {
				if *stmt, err = tx.PrepareContext(ctx, insertDNS(len(args)/7)); err != nil {
					return err
				}
			}
			if _, err := (*stmt).ExecContext(ctx, args...); err != nil {
				return err
			}
			args = args[:0]
		}
		return lk.flush(ctx)
	})
	if err != nil {
		return fmt.Errorf("store: insert dns: %w", err)
	}
	lk.commit()
	return nil
}

// insertRows is how many lookups one INSERT statement carries.
const insertRows = 64

// insertDNS returns an INSERT statement for n lookups.
func insertDNS(n int) string {
	var b strings.Builder
	b.WriteString(`INSERT INTO dns(ts, seq, client, domain_id, qtype, blocked, source_id) VALUES `)
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`(?, ?, ?, ?, ?, ?, ?)`)
	}
	return b.String()
}

// DNSBetween returns the lookups in p, oldest first.
//
// A month of a busy home is over a million rows, so this is the store's
// hottest read. Rather than joining every row to domains and sources (two
// b-tree lookups per row), it reads the ids and resolves them from maps
// loaded up front, and it sizes the result from a count so the slice is
// allocated once instead of grown by doubling. It runs in one read
// transaction, so the names, the count and the rows are one snapshot even
// while Prune deletes domains and ingestion inserts.
func (s *Store) DNSBetween(ctx context.Context, p model.Period) ([]model.DNSQuery, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("store: dns between: %w", err)
	}
	defer tx.Rollback()
	from, to := nanos(p.From), nanos(p.To)

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM dns WHERE ts >= ? AND ts < ?`, from, to).Scan(&n); err != nil {
		return nil, fmt.Errorf("store: dns between: %w", err)
	}
	if n == 0 {
		return nil, nil
	}
	srcNames, err := names(ctx, tx, `SELECT id, name FROM sources`)
	if err != nil {
		return nil, fmt.Errorf("store: dns between: %w", err)
	}
	// domains.last_ts is at or after every lookup of the domain, so only
	// domains with last_ts in or after p can be referred to.
	domNames, err := names(ctx, tx, `SELECT id, name FROM domains WHERE last_ts >= ?`, from)
	if err != nil {
		return nil, fmt.Errorf("store: dns between: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT ts, client, domain_id, qtype, blocked, source_id
		FROM dns
		WHERE ts >= ? AND ts < ?
		ORDER BY ts, seq`, from, to)
	if err != nil {
		return nil, fmt.Errorf("store: dns between: %w", err)
	}
	defer rows.Close()

	// qtype strings repeat heavily; interning them keeps a month of
	// lookups from holding a million copies of "A" and "AAAA".
	qtypes := make(map[string]string)
	out := make([]model.DNSQuery, 0, n)
	for rows.Next() {
		var (
			ts, dom, src int64
			client, qt   sql.RawBytes
			blocked      bool
		)
		if err := rows.Scan(&ts, &client, &dom, &qt, &blocked, &src); err != nil {
			return nil, fmt.Errorf("store: dns between: %w", err)
		}
		qtype, ok := qtypes[string(qt)]
		if !ok {
			qtype = string(qt)
			qtypes[qtype] = qtype
		}
		domain, ok := domNames[dom]
		if !ok {
			// Not expected (InsertDNS keeps last_ts current), but never
			// return a lookup without its domain.
			if err := tx.QueryRowContext(ctx, `SELECT name FROM domains WHERE id = ?`, dom).Scan(&domain); err != nil {
				return nil, fmt.Errorf("store: dns between: domain %d: %w", dom, err)
			}
			domNames[dom] = domain
		}
		out = append(out, model.DNSQuery{
			Time:     fromNanos(ts),
			ClientIP: ipFrom(client),
			Domain:   domain,
			QType:    qtype,
			Blocked:  blocked,
			Source:   srcNames[src],
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: dns between: %w", err)
	}
	return out, nil
}

// names runs an "id, name" query and returns the result as a map.
func names(ctx context.Context, tx *sql.Tx, q string, args ...any) (map[int64]string, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[int64]string)
	for rows.Next() {
		var (
			id   int64
			name string
		)
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		m[id] = name
	}
	return m, rows.Err()
}
