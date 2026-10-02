package store

import (
	"context"
	"database/sql"
	"fmt"

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
		ins, err := tx.PrepareContext(ctx,
			`INSERT INTO dns(ts, seq, client, domain_id, qtype, blocked, source_id) VALUES (?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		for _, q := range qs {
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
			if _, err := ins.ExecContext(ctx, ts, seq, ipBytes(q.ClientIP), dom, q.QType, q.Blocked, src); err != nil {
				return err
			}
		}
		return lk.flush(ctx)
	})
	if err != nil {
		return fmt.Errorf("store: insert dns: %w", err)
	}
	lk.commit()
	return nil
}

// DNSBetween returns the lookups in p, oldest first.
func (s *Store) DNSBetween(ctx context.Context, p model.Period) ([]model.DNSQuery, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT q.ts, q.client, d.name, q.qtype, q.blocked, s.name
		FROM dns q
		JOIN domains d ON d.id = q.domain_id
		JOIN sources s ON s.id = q.source_id
		WHERE q.ts >= ? AND q.ts < ?
		ORDER BY q.ts, q.seq`, nanos(p.From), nanos(p.To))
	if err != nil {
		return nil, fmt.Errorf("store: dns between: %w", err)
	}
	defer rows.Close()

	// Domain, qtype and source strings repeat heavily; interning them keeps
	// a week of lookups from holding a million copies of the same names.
	intern := make(map[string]string)
	str := func(b sql.RawBytes) string {
		if v, ok := intern[string(b)]; ok {
			return v
		}
		v := string(b)
		intern[v] = v
		return v
	}
	var out []model.DNSQuery
	for rows.Next() {
		var (
			ts                   int64
			client, dom, qt, src sql.RawBytes
			blocked              bool
		)
		if err := rows.Scan(&ts, &client, &dom, &qt, &blocked, &src); err != nil {
			return nil, fmt.Errorf("store: dns between: %w", err)
		}
		out = append(out, model.DNSQuery{
			Time:     fromNanos(ts),
			ClientIP: ipFrom(client),
			Domain:   str(dom),
			QType:    str(qt),
			Blocked:  blocked,
			Source:   str(src),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: dns between: %w", err)
	}
	return out, nil
}
