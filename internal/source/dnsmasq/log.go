// Package dnsmasq reads DNS lookups from a dnsmasq query log.
package dnsmasq

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/source"
)

// lookahead is how many lines after a query its answer may appear. Queries
// still unanswered by then are taken as not blocked: blocks are answered
// from config immediately, so a late answer came from upstream.
const lookahead = 100

// cnameHold is how many lines a query answered with a CNAME is held back,
// at most. Pi-hole decides whether to block a CNAME chain only after
// logging it ("reply a.example is <CNAME>", then the chain, then "gravity
// blocked a.example is 0.0.0.0"), all while handling one reply, so the
// hold also ends as soon as the next query is logged.
const cnameHold = 16

// Option configures a Log.
type Option func(*Log)

// WithName overrides the source name (default "dnsmasq").
func WithName(name string) Option {
	return func(l *Log) { l.name = name }
}

// WithLocation sets the time zone of timestamps written without one
// (classic syslog and log-facility lines). The default is time.Local.
func WithLocation(loc *time.Location) Option {
	return func(l *Log) { l.loc = loc }
}

// Log reads a file dnsmasq writes with log-queries enabled, as a
// source.DNSSource: a syslog file (/var/log/syslog, /var/log/messages), a
// log-facility=<file>, Pi-hole's /var/log/pihole/pihole.log, or OpenWrt's
// logread output saved to a file. Lines from other programs are ignored.
//
// A query counts as blocked when dnsmasq answers it from its own
// configuration with 0.0.0.0, ::, NXDOMAIN or NODATA (the address=/name/
// lines blocklists generate), or when Pi-hole logs it as blocked. Queries
// are paired with their answers by the serial number log-queries=extra
// adds; without it, by name, which is right except when two clients ask for
// the same name at the same moment. A side effect of the rule is that names
// answered NXDOMAIN by local=/lan/ also count as blocked.
//
// The cursor is "<inode>:<offset>". When the file is rotated (its inode
// changes) the rest of the old file is read from <path>.1 if it is there,
// then the new file from the start; when it is truncated in place
// (copytruncate) reading restarts at the top, and lines written between the
// last read and the truncation are lost.
type Log struct {
	path string
	name string
	loc  *time.Location
	now  func() time.Time
}

// NewLog returns a reader for the dnsmasq log at path.
func NewLog(path string, opts ...Option) *Log {
	l := &Log{path: path, name: "dnsmasq", loc: time.Local, now: time.Now}
	for _, o := range opts {
		o(l)
	}
	return l
}

// Name implements source.DNSSource.
func (l *Log) Name() string { return l.name }

type position struct {
	inode  uint64
	offset int64
}

func (p position) String() string {
	return strconv.FormatUint(p.inode, 10) + ":" + strconv.FormatInt(p.offset, 10)
}

func parseCursor(c string) (position, error) {
	if c == "" {
		return position{}, nil
	}
	inos, offs, ok := strings.Cut(c, ":")
	ino, err := strconv.ParseUint(inos, 10, 64)
	off, err2 := strconv.ParseInt(offs, 10, 64)
	if !ok || err != nil || err2 != nil || off < 0 {
		return position{}, fmt.Errorf("dnsmasq: bad cursor %q: %w", c, source.ErrBadCursor)
	}
	return position{ino, off}, nil
}

// segment is a stretch of a file to read. Only the live log can still be
// growing; a rotated one is complete.
type segment struct {
	f     *os.File
	start position
	live  bool
}

// FetchDNS implements source.DNSSource. The cursor may advance past lines
// that hold no queries even when no records are returned. A query whose
// answer has not been logged yet is held back until it has.
func (l *Log) FetchDNS(ctx context.Context, cursor string, limit int) ([]model.DNSQuery, string, error) {
	cur, err := parseCursor(cursor)
	if err != nil {
		return nil, cursor, err
	}
	if limit <= 0 {
		return nil, cursor, nil
	}
	f, err := os.Open(l.path)
	if err != nil {
		return nil, cursor, fmt.Errorf("dnsmasq: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, cursor, fmt.Errorf("dnsmasq: %w", err)
	}
	ino := inode(fi)

	var segs []segment
	switch {
	case cursor != "" && cur.inode == ino && cur.offset <= fi.Size():
		segs = []segment{{f, cur, true}}
	case cursor != "" && cur.inode != ino:
		if old := l.rotated(cur.inode); old != nil {
			defer old.Close()
			segs = append(segs, segment{old, cur, false})
		}
		fallthrough
	default: // first run, truncated, or rotated away
		segs = append(segs, segment{f, position{ino, 0}, true})
	}

	r := reader{name: l.name, loc: l.loc, now: l.now(), limit: limit}
	next := cur
	for _, s := range segs {
		if next, err = r.read(ctx, s); err != nil {
			return nil, cursor, err
		}
		if r.full() {
			break
		}
	}
	if len(r.out) == 0 && (next == cur || next.offset == 0) {
		return nil, cursor, nil
	}
	return r.out, next.String(), nil
}

// rotated opens <path>.1 if it is the file the cursor was reading.
func (l *Log) rotated(ino uint64) *os.File {
	f, err := os.Open(l.path + ".1")
	if err != nil {
		return nil
	}
	if fi, err := f.Stat(); err == nil && ino != 0 && inode(fi) == ino {
		return f
	}
	f.Close()
	return nil
}

// pending is a query waiting for the line that says how it was answered.
type pending struct {
	e        event
	end      int64 // offset just past its line
	line     int
	answered bool
	hold     int // answered with a CNAME: line number up to which to wait
}

type reader struct {
	name  string
	loc   *time.Location
	now   time.Time
	limit int
	out   []model.DNSQuery
}

func (r *reader) full() bool { return len(r.out) >= r.limit }

// read processes one segment and returns the position up to which every
// line has been dealt with.
func (r *reader) read(ctx context.Context, s segment) (position, error) {
	fi, err := s.f.Stat()
	if err != nil {
		return s.start, fmt.Errorf("dnsmasq: %w", err)
	}
	var (
		br      = bufio.NewReaderSize(io.NewSectionReader(s.f, s.start.offset, fi.Size()-s.start.offset), 1<<16)
		offset  = s.start.offset
		done    = s.start
		queue   []pending
		lineNum int
	)
	// emit hands over queries from the head of the queue that are settled
	// (or all of them, at the end of a complete file).
	emit := func(all bool) {
		for len(queue) > 0 && !r.full() {
			p := queue[0]
			if !all && (!p.answered && lineNum-p.line < lookahead || p.answered && lineNum < p.hold) {
				return
			}
			r.out = append(r.out, model.DNSQuery{
				Time:     p.e.time,
				ClientIP: p.e.client,
				Domain:   p.e.name,
				QType:    p.e.qtype,
				Blocked:  p.e.blocked,
				Source:   r.name,
			})
			done.offset = p.end
			queue = queue[1:]
		}
	}

	for !r.full() {
		if lineNum%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return s.start, err
			}
		}
		line, err := readLine(br)
		if errors.Is(err, io.EOF) && (s.live || len(line) == 0) {
			// The live log's unterminated last line is still being written.
			break
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return s.start, fmt.Errorf("dnsmasq: read %s: %w", s.f.Name(), err)
		}
		offset += int64(len(line))
		lineNum++
		if e, ok := parseLine(string(line), r.loc, r.now); ok {
			if e.query {
				for i := range queue {
					queue[i].hold = 0
				}
				queue = append(queue, pending{e: e, end: offset, line: lineNum})
			} else {
				answer(queue, e, lineNum)
			}
		}
		emit(false)
		if len(queue) == 0 {
			done.offset = offset
		}
	}
	if !s.live {
		emit(true)
		if len(queue) == 0 {
			done.offset = offset
		}
	}
	return done, nil
}

// answer settles the oldest unanswered query e refers to. A blocking
// verdict for a query already answered with a CNAME (and still held back)
// overrides that answer and releases it.
func answer(queue []pending, e event, lineNum int) {
	matches := func(p *pending) bool {
		if p.e.serial != "" && e.serial != "" {
			return p.e.serial == e.serial
		}
		return p.e.key == e.key
	}
	for i := range queue {
		p := &queue[i]
		if p.answered || !matches(p) {
			continue
		}
		p.answered, p.e.blocked = true, e.blocked
		if e.cname {
			p.hold = lineNum + cnameHold
		}
		return
	}
	if !e.blocked {
		return
	}
	for i := range queue {
		if p := &queue[i]; p.hold > 0 && matches(p) {
			p.e.blocked, p.hold = true, 0 // the verdict is final
			return
		}
	}
}

// readLine returns the next line including its newline, however long.
func readLine(br *bufio.Reader) ([]byte, error) {
	line, err := br.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		rest, err := br.ReadBytes('\n')
		return append(bytes.Clone(line), rest...), err
	}
	return line, err
}
