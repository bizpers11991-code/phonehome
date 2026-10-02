// Package conntrack turns snapshots of the Linux connection-tracking table
// into model.Flow records: which device on the LAN opened a connection to
// which public address and port.
//
// It reads /proc/net/nf_conntrack, or the output of `conntrack -L` (with or
// without `-o extended`), which has the same entry format. Only TCP and UDP
// connections from a private, unique-local or link-local address to a public
// one are kept; traffic inside the LAN, the router's own connections and
// inbound port forwards are dropped. Home IPv6 devices usually have global
// addresses; pass the LAN's delegated prefix with WithLocalPrefixes so their
// connections count too.
//
// A snapshot says what is open now, not when it opened, so a connection is
// reported the first time a poll sees it, with Start set to that poll's time
// and its byte counters as they were at that moment. Connections are
// remembered in memory while they stay in the table, so a long download is
// reported once; if the same 5-tuple disappears and comes back, it is a new
// connection. Because that memory is lost on restart, connections still open
// across a restart are reported a second time, at most once each.
//
// Byte counts are zero unless the kernel's accounting is on:
//
//	sysctl -w net.netfilter.nf_conntrack_acct=1
package conntrack

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// DefaultPath is where the kernel exposes the conntrack table.
const DefaultPath = "/proc/net/nf_conntrack"

// Table polls the conntrack table. It implements source.FlowSource and is
// safe for concurrent use.
type Table struct {
	name  string
	open  func(context.Context) (io.ReadCloser, error)
	now   func() time.Time
	local []netip.Prefix // extra LAN prefixes besides private and link-local

	mu      sync.Mutex
	seen    map[tuple]struct{} // connections present in the previous snapshot
	pending []model.Flow       // new flows not yet handed out because of limit
}

// Option configures a Table.
type Option func(*Table)

// WithClock replaces time.Now, for tests.
func WithClock(now func() time.Time) Option {
	return func(t *Table) { t.now = now }
}

// WithLocalPrefixes treats addresses in prefixes as devices on the LAN, in
// addition to private (RFC 1918, ULA) and link-local ones. Use it for the
// globally routable IPv6 prefix your ISP delegates to the LAN.
func WithLocalPrefixes(prefixes ...netip.Prefix) Option {
	return func(t *Table) { t.local = append(t.local, prefixes...) }
}

// WithReader makes the Table read each snapshot from open instead of the
// file at path. The returned reader is closed after every poll.
func WithReader(open func(context.Context) (io.ReadCloser, error)) Option {
	return func(t *Table) { t.open = open }
}

// Command makes the Table run a command for each snapshot, typically
// Command("conntrack", "-L", "-o", "extended"), which works where
// /proc/net/nf_conntrack is not available. It needs CAP_NET_ADMIN.
func Command(name string, arg ...string) Option {
	return func(t *Table) {
		t.name = "conntrack:" + filepath.Base(name)
		t.open = func(ctx context.Context) (io.ReadCloser, error) {
			out, err := exec.CommandContext(ctx, name, arg...).Output()
			if err != nil {
				return nil, err
			}
			return io.NopCloser(bytes.NewReader(out)), nil
		}
	}
}

// New returns a Table reading snapshots from path (usually DefaultPath).
func New(path string, opts ...Option) *Table {
	t := &Table{
		name: "conntrack:" + filepath.Base(path),
		open: func(context.Context) (io.ReadCloser, error) { return os.Open(path) },
		now:  time.Now,
		seen: map[tuple]struct{}{},
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Name identifies the source, e.g. "conntrack:nf_conntrack".
func (t *Table) Name() string { return t.name }

// FetchFlows returns connections that appeared since the previous poll,
// oldest first, at most limit of them (limit <= 0 means no limit). Flows
// beyond limit are kept and returned by the next calls before polling again.
//
// The returned cursor is the time of the poll, for display only: the
// connection memory lives in the Table, so cursor is not interpreted.
func (t *Table) FetchFlows(ctx context.Context, cursor string, limit int) ([]model.Flow, string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.pending) == 0 {
		if err := t.poll(ctx); err != nil {
			return nil, cursor, err
		}
	}
	if len(t.pending) == 0 {
		return nil, cursor, nil
	}
	n := len(t.pending)
	if limit > 0 && limit < n {
		n = limit
	}
	out := t.pending[:n:n]
	t.pending = t.pending[n:]
	return out, out[len(out)-1].Start.Format(time.RFC3339Nano), nil
}

// poll reads one snapshot, queues the connections not seen in the previous
// one, and forgets the connections that have closed.
func (t *Table) poll(ctx context.Context) error {
	rc, err := t.open(ctx)
	if err != nil {
		return err
	}
	defer rc.Close()
	at := t.now()
	current := map[tuple]struct{}{}
	var fresh []model.Flow
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		e, ok := parseEntry(sc.Text())
		if !ok || !t.outbound(e.orig) {
			continue
		}
		if _, dup := current[e.orig]; dup {
			continue
		}
		current[e.orig] = struct{}{}
		if _, old := t.seen[e.orig]; old {
			continue
		}
		fresh = append(fresh, model.Flow{
			Start:      at,
			ClientIP:   e.orig.src,
			RemoteIP:   e.orig.dst,
			RemotePort: e.orig.dport,
			Proto:      e.orig.proto,
			BytesOut:   e.bytesOut,
			BytesIn:    e.bytesIn,
			Source:     t.name,
		})
	}
	if err := sc.Err(); err != nil {
		return err
	}
	t.seen = current
	t.pending = fresh
	return nil
}

// tuple identifies a connection by its original-direction 5-tuple.
type tuple struct {
	proto        string
	src, dst     netip.Addr
	sport, dport uint16
}

// entry is one parsed conntrack line.
type entry struct {
	orig              tuple
	bytesOut, bytesIn uint64
}

// parseEntry parses one line such as
//
//	ipv4 2 tcp 6 431999 ESTABLISHED src=192.168.1.20 dst=93.184.215.14 sport=50412 dport=443 packets=12 bytes=3456 src=93.184.215.14 dst=203.0.113.7 sport=443 dport=50412 packets=10 bytes=7890 [ASSURED] mark=0 use=2
//
// The leading "ipv4 2" is absent in plain `conntrack -L` output. The first
// src/dst/sport/dport/bytes belong to the original direction, the second set
// to the reply. It reports false for anything but TCP and UDP and for lines
// that are not conntrack entries.
func parseEntry(line string) (entry, bool) {
	f := strings.Fields(line)
	if len(f) >= 2 && (f[0] == "ipv4" || f[0] == "ipv6") {
		f = f[2:]
	}
	if len(f) < 3 || (f[0] != "tcp" && f[0] != "udp") {
		return entry{}, false
	}
	e := entry{orig: tuple{proto: f[0]}}
	reply := false
	for _, kv := range f[3:] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue // TCP state, [ASSURED], [UNREPLIED]
		}
		var err error
		switch k {
		case "src":
			if e.orig.src.IsValid() {
				reply = true
			} else {
				e.orig.src, err = netip.ParseAddr(v)
			}
		case "dst":
			if !reply {
				e.orig.dst, err = netip.ParseAddr(v)
			}
		case "sport":
			if !reply {
				e.orig.sport, err = parsePort(v)
			}
		case "dport":
			if !reply {
				e.orig.dport, err = parsePort(v)
			}
		case "bytes":
			var n uint64
			n, err = strconv.ParseUint(v, 10, 64)
			if reply {
				e.bytesIn = n
			} else {
				e.bytesOut = n
			}
		}
		if err != nil {
			return entry{}, false
		}
	}
	if !e.orig.src.IsValid() || !e.orig.dst.IsValid() {
		return entry{}, false
	}
	e.orig.src, e.orig.dst = e.orig.src.Unmap(), e.orig.dst.Unmap()
	return e, true
}

func parsePort(s string) (uint16, error) {
	n, err := strconv.ParseUint(s, 10, 16)
	return uint16(n), err
}

// sharedSpace is the carrier-grade NAT range (RFC 6598): neither a LAN
// address nor the public internet.
var sharedSpace = netip.MustParsePrefix("100.64.0.0/10")

// outbound reports whether c goes from a device on the LAN to the internet.
func (t *Table) outbound(c tuple) bool {
	return t.isLocal(c.src) && !t.isLocal(c.dst) &&
		c.dst.IsGlobalUnicast() && !sharedSpace.Contains(c.dst)
}

func (t *Table) isLocal(a netip.Addr) bool {
	if a.IsPrivate() || a.IsLinkLocalUnicast() {
		return true
	}
	for _, p := range t.local {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
