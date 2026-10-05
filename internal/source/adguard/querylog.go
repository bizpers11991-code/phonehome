// Package adguard reads DNS lookups from AdGuard Home's query log.
package adguard

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/source"
)

// seekSlack is how far before the cursor's time reading starts. AdGuard
// stamps each entry with the time the query arrived but appends it when the
// answer is ready, so a file is only roughly in time order: a slow upstream
// answer lands after faster, later queries.
const seekSlack = time.Minute

// Option configures a QueryLog.
type Option func(*QueryLog)

// WithName overrides the source name (default "adguard").
func WithName(name string) Option {
	return func(l *QueryLog) { l.name = name }
}

// QueryLog reads AdGuard Home's querylog.json (JSON lines, in
// AdGuardHome/data/ by default) and its rotated predecessor querylog.json.1
// as a source.DNSSource. AdGuard keeps recent entries in memory and appends
// them in batches (querylog.size_memory), so lookups may show up late, and
// they never show up if the query log is disabled or memory-only
// (querylog.file_enabled: false).
//
// The cursor is "<RFC3339Nano time>|<n>", naming the last entry read as the
// n-th entry stamped with exactly that time. Positions are tracked in file
// order rather than time order because entries are not strictly sorted;
// rotation keeps file order intact, so the cursor survives it.
type QueryLog struct {
	path string
	name string
}

// NewQueryLog returns a reader for the query log at path, normally
// ".../AdGuardHome/data/querylog.json". path+".1" is read first if present.
func NewQueryLog(path string, opts ...Option) *QueryLog {
	l := &QueryLog{path: path, name: "adguard"}
	for _, o := range opts {
		o(l)
	}
	return l
}

// Name implements source.DNSSource.
func (l *QueryLog) Name() string { return l.name }

// entry is the part of AdGuard's logEntry (internal/querylog) we use.
type entry struct {
	T      time.Time `json:"T"`
	QH     string    `json:"QH"`
	QT     string    `json:"QT"`
	IP     string    `json:"IP"`
	Result struct {
		IsFiltered       bool   `json:"IsFiltered"`
		Reason           reason `json:"Reason"`
		DNSRewriteResult *struct {
			RCode int `json:"RCode"`
		} `json:"DNSRewriteResult"`
	} `json:"Result"`
}

// reason is filtering.Reason. The file stores the number; the string form
// is what AdGuard's HTTP API shows, accepted here too for robustness.
type reason int

// Filtering reasons that mean the query was refused or sinkholed, from the
// iota block in AdGuardHome/internal/filtering. SafeSearch (7) and the
// rewrites (9–11) answer the query, so they are not blocks, except a
// $dnsrewrite rule that answers with an error code (11 with an RCode, as
// in "||example.org^$dnsrewrite=REFUSED"), which is how such rules block.
const (
	filteredBlockList      reason = 3
	filteredSafeBrowsing   reason = 4
	filteredParental       reason = 5
	filteredInvalid        reason = 6
	filteredBlockedService reason = 8
	rewrittenRule          reason = 11
)

var reasonNames = map[string]reason{
	"FilteredBlackList":      filteredBlockList,
	"FilteredBlockList":      filteredBlockList,
	"FilteredSafeBrowsing":   filteredSafeBrowsing,
	"FilteredParental":       filteredParental,
	"FilteredInvalid":        filteredInvalid,
	"FilteredBlockedService": filteredBlockedService,
}

func (r *reason) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*r = reasonNames[s]
		return nil
	}
	n, err := strconv.Atoi(string(b))
	*r = reason(n)
	return err
}

func (e *entry) blocked() bool {
	switch e.Result.Reason {
	case filteredBlockList, filteredSafeBrowsing, filteredParental, filteredInvalid, filteredBlockedService:
		return e.Result.IsFiltered
	case rewrittenRule:
		return e.Result.DNSRewriteResult != nil && e.Result.DNSRewriteResult.RCode != 0
	}
	return false
}

// position identifies an entry as the n-th one stamped with time t.
type position struct {
	t time.Time
	n int
}

func parseCursor(c string) (position, error) {
	if c == "" {
		return position{}, nil
	}
	ts, ns, ok := strings.Cut(c, "|")
	t, err := time.Parse(time.RFC3339Nano, ts)
	n, err2 := strconv.Atoi(ns)
	if !ok || err != nil || err2 != nil || n < 1 {
		return position{}, fmt.Errorf("adguard: bad cursor %q: %w", c, source.ErrBadCursor)
	}
	return position{t, n}, nil
}

func (p position) String() string {
	return p.t.Format(time.RFC3339Nano) + "|" + strconv.Itoa(p.n)
}

// FetchDNS implements source.DNSSource. Malformed lines and entries without
// a parsable client IP are skipped. If the cursor's entry is gone (the log
// was cleared, or rotated twice since the last call) reading resumes with
// the first entry stamped later than the cursor.
func (l *QueryLog) FetchDNS(ctx context.Context, cursor string, limit int) ([]model.DNSQuery, string, error) {
	cur, err := parseCursor(cursor)
	if err != nil {
		return nil, cursor, err
	}
	if limit <= 0 {
		return nil, cursor, nil
	}
	files, err := l.open()
	if err != nil {
		return nil, cursor, err
	}
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()

	s, err := l.scan(ctx, files, cur, cursor == "", limit, true)
	if err == nil && !s.found && s.seeked {
		// The binary search assumes entries are roughly in time order. A
		// clock that jumped breaks that, so look through whole files once
		// before deciding the cursor's entry is gone.
		s, err = l.scan(ctx, files, cur, false, limit, false)
	}
	if err != nil {
		return nil, cursor, err
	}
	if !s.found {
		switch {
		case s.orphansLast != (position{}):
			// The cursor's entry has vanished; fall back to time order.
			s.out, s.last, s.consumed = s.orphans, s.orphansLast, true
		case s.entries > 0:
			// It has vanished and every entry left is stamped before it:
			// the clock was set back, then the log rotated. Entries only
			// leave the log from the front, so all of these were written
			// after the cursor's entry; read them from the start rather
			// than wait for the clock to catch up.
			if s, err = l.scan(ctx, files, position{}, true, limit, false); err != nil {
				return nil, cursor, err
			}
		}
	}
	if !s.consumed {
		return nil, cursor, nil
	}
	return s.out, s.last.String(), nil
}

// scan reads files in order from the cursor, binary-searching each file
// for a starting point when seek is set.
func (l *QueryLog) scan(ctx context.Context, files []*os.File, cur position, found bool, limit int, seek bool) (*scan, error) {
	s := &scan{cur: cur, found: found, limit: limit, name: l.name, counts: map[int64]int{}, seek: seek}
	for _, f := range files {
		if err := s.file(ctx, f); err != nil {
			return nil, err
		}
		if s.full() {
			break
		}
	}
	return s, nil
}

// open opens querylog.json.1 (if any) and querylog.json, retrying if
// AdGuard rotates between the two opens, which would otherwise make us
// skip the file that just became .1.
func (l *QueryLog) open() ([]*os.File, error) {
	for range 3 {
		old, err := os.Open(l.path + ".1")
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("adguard: %w", err)
		}
		cur, err := os.Open(l.path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			if old != nil {
				old.Close()
			}
			return nil, fmt.Errorf("adguard: %w", err)
		}
		var files []*os.File
		for _, f := range []*os.File{old, cur} {
			if f != nil {
				files = append(files, f)
			}
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("adguard: no query log at %s (is querylog.file_enabled on?)", l.path)
		}
		if old == nil || stillRotated(old, l.path+".1") {
			return files, nil
		}
		for _, f := range files {
			f.Close()
		}
	}
	return nil, errors.New("adguard: query log kept rotating while being opened")
}

func stillRotated(f *os.File, path string) bool {
	a, err1 := f.Stat()
	b, err2 := os.Stat(path)
	return err1 == nil && err2 == nil && os.SameFile(a, b)
}

// scan walks entries in file order. Entries up to and including the cursor
// entry were read before; those after it are collected. Until the cursor
// entry turns up, entries stamped after it are kept aside (as orphans) in
// case it never does.
type scan struct {
	cur   position
	found bool
	limit int
	name  string
	seek  bool // start each file near the cursor's time

	seeked  bool // some file was read from past its start
	entries int  // parsable entries seen

	// counts numbers the entries stamped with each instant, from where
	// the scan started, exactly as the next scan will number them. Every
	// entry is counted, so that an entry stamped with the same instant as
	// one before the cursor (after the clock went back) gets a number of
	// its own instead of the earlier entry's.
	counts map[int64]int

	out      []model.DNSQuery
	last     position // the last consumed entry
	consumed bool

	orphans     []model.DNSQuery
	orphansLast position
	orphansDone bool
}

func (s *scan) full() bool { return s.found && len(s.out) >= s.limit }

// file reads one log file from slightly before the cursor's time.
func (s *scan) file(ctx context.Context, f *os.File) error {
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("adguard: %w", err)
	}
	var start int64
	if s.seek && !s.cur.t.IsZero() {
		start = seek(f, fi.Size(), s.cur.t.Add(-seekSlack))
		s.seeked = s.seeked || start > 0
	}
	r := bufio.NewReaderSize(io.NewSectionReader(f, start, fi.Size()-start), 1<<16)
	for i := 0; ; i++ {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		line, _, err := source.ReadLine(r)
		if err != nil {
			// io.EOF: whatever is left has no newline yet, so AdGuard is
			// still writing it; it is read next time.
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("adguard: read %s: %w", f.Name(), err)
		}
		var e entry // a nil line was too long, and is skipped
		if line == nil || json.Unmarshal(line, &e) != nil || e.T.IsZero() {
			continue
		}
		s.add(&e)
		if s.full() {
			return nil
		}
	}
}

func (s *scan) add(e *entry) {
	k := e.T.UnixNano()
	s.counts[k]++
	s.entries++
	if !s.found {
		switch {
		case e.T.Equal(s.cur.t):
			s.found = s.counts[k] == s.cur.n
		case e.T.After(s.cur.t) && !s.orphansDone:
			if len(s.orphans) >= s.limit {
				s.orphansDone = true
				return
			}
			s.orphansLast = position{e.T, s.counts[k]}
			if q, ok := s.query(e); ok {
				s.orphans = append(s.orphans, q)
			}
		}
		return
	}
	s.last, s.consumed = position{e.T, s.counts[k]}, true
	if q, ok := s.query(e); ok {
		s.out = append(s.out, q)
	}
}

func (s *scan) query(e *entry) (model.DNSQuery, bool) {
	ip, err := netip.ParseAddr(e.IP)
	dom := source.Domain(e.QH)
	if err != nil || dom == "" {
		return model.DNSQuery{}, false
	}
	return model.DNSQuery{
		Time:     e.T,
		ClientIP: ip.WithZone("").Unmap(),
		Domain:   dom,
		QType:    e.QT,
		Blocked:  e.blocked(),
		Source:   s.name,
	}, true
}

// seek returns the offset of a line start at or before the first entry
// stamped at or after target, by binary search over the roughly
// time-ordered file, so long logs are not read from the top every time.
func seek(f io.ReaderAt, size int64, target time.Time) int64 {
	const window = 64 << 10
	lo, hi := int64(0), size
	for hi-lo > window {
		mid := lo + (hi-lo)/2
		off, t, ok := nextEntry(f, mid, hi)
		switch {
		case !ok:
			hi = mid
		case t.Before(target):
			lo = off
		default:
			hi = mid
		}
	}
	return lo
}

// nextEntry finds the first line starting after offset from (and before
// to) that holds a parsable entry, returning its offset and time.
func nextEntry(f io.ReaderAt, from, to int64) (int64, time.Time, bool) {
	r := bufio.NewReader(io.NewSectionReader(f, from, to-from))
	off := from
	for {
		skip, err := r.ReadSlice('\n')
		off += int64(len(skip))
		if err == nil {
			break
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return 0, time.Time{}, false
		}
	}
	for {
		line, n, err := source.ReadLine(r)
		if err != nil {
			return 0, time.Time{}, false
		}
		var e struct {
			T time.Time `json:"T"`
		}
		if line != nil && json.Unmarshal(line, &e) == nil && !e.T.IsZero() {
			return off, e.T, true
		}
		off += int64(n)
	}
}
