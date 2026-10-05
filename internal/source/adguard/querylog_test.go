package adguard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/source"
)

var _ source.DNSSource = (*QueryLog)(nil)

// want is the testdata log as "domain qtype client blocked", oldest file
// first and in file order. Malformed lines, an entry without a client and
// the half-written last line are absent.
var want = []string{
	"acr-eu-prd.samsungcloud.tv A 192.168.1.20 true",
	"time.samsungcloudsolution.com AAAA 192.168.1.20 false",
	"device-metrics-us.amazon.com HTTPS fe80::1c2b:3aff:fe4d:5e6f true",
	"slow.example.org A 192.168.1.30 false", // stamped earlier than the line above it
	"www.google.com A 192.168.1.30 false",   // SafeSearch rewrite, not a block
	"unifi.ui.com A 192.168.1.40 false",     // three entries at the same instant
	"trace.svc.ui.com A 192.168.1.40 true",
	"fw-update.ubnt.com AAAA 192.168.1.40 false",
	"log-config.samsungacr.com A 192.168.1.20 true",
}

// TestUpstreamFormat reads testdata/upstream/querylog.json. All but its
// last line were written by AdGuard Home's own querylog code (newLogEntry
// and flushLogBuffer at commit fe415923abb6, October 2026) from one
// scenario each; the last is the older Rule/FilterID result format that
// AdGuard Home still decodes. A query for the root (QH ".") is skipped.
func TestUpstreamFormat(t *testing.T) {
	l := NewQueryLog(filepath.Join("testdata", "upstream", "querylog.json"))
	qs, _, err := l.FetchDNS(context.Background(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, summary(qs), []string{
		"www.example.org A 192.168.1.20 false",
		"www.example.org AAAA fd00::20 false", // cached
		"ads.example.net A 192.168.1.21 true",
		"metrics.vendor.example A 192.168.1.21 true", // its CNAME target was blocked
		"www.tiktok.com HTTPS 192.168.1.22 true",     // blocked service
		"malware.example A 192.168.1.22 true",        // safe browsing
		"adult.example A 192.168.1.22 true",          // parental control
		"www.google.com A 192.168.1.22 false",        // safe search
		"unifi.ui.com A 192.168.1.23 false",          // allowlisted
		"nas.home A 192.168.1.23 false",              // DNS rewrite
		"router.lan A 192.168.1.23 false",            // /etc/hosts
		"telemetry.example.com A 192.168.1.24 true",  // $dnsrewrite=REFUSED
		"beacon.example.com AAAA 192.168.1.24 true",  // $dnsrewrite=NXDOMAIN
		"printer.example.com A 192.168.1.24 false",   // $dnsrewrite to an address
		"example.net TYPE65534 192.168.1.25 false",   // a DoH client with a ClientID
		"legacy.ads.example A 192.168.1.26 true",
	})
	if want := time.Date(2026, 10, 2, 11, 58, 1, 123456789, time.UTC); !qs[0].Time.Equal(want) {
		t.Errorf("time = %v, want %v", qs[0].Time, want)
	}
}

func summary(qs []model.DNSQuery) []string {
	var out []string
	for _, q := range qs {
		if q.Source != "adguard" {
			panic("wrong source " + q.Source)
		}
		out = append(out, fmt.Sprintf("%s %s %s %t", q.Domain, q.QType, q.ClientIP, q.Blocked))
	}
	return out
}

func equal(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// copyTestdata copies the testdata logs into a fresh directory, so tests
// can append to and rotate them.
func copyTestdata(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"querylog.json", "querylog.json.1"} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "querylog.json")
}

// drain reads everything with the given batch size.
func drain(t *testing.T, l *QueryLog, cursor string, limit int) ([]string, string) {
	t.Helper()
	var all []string
	for range 1000 {
		qs, next, err := l.FetchDNS(context.Background(), cursor, limit)
		if err != nil {
			t.Fatal(err)
		}
		if len(qs) > limit {
			t.Fatalf("got %d records, limit %d", len(qs), limit)
		}
		if qs == nil && next == cursor {
			return all, cursor
		}
		all = append(all, summary(qs)...)
		cursor = next
	}
	t.Fatal("cursor never settled")
	return nil, ""
}

func TestFetchAll(t *testing.T) {
	l := NewQueryLog(filepath.Join("testdata", "querylog.json"))
	qs, cur, err := l.FetchDNS(context.Background(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, summary(qs), want)
	if cur != "2026-10-02T00:00:03+02:00|1" {
		t.Fatalf("cursor = %q", cur)
	}
	if first := time.Date(2026, 10, 1, 20, 0, 0, 100e6, time.UTC); !qs[0].Time.Equal(first) {
		t.Fatalf("first time = %v, want %v", qs[0].Time, first)
	}
}

func TestFetchInSmallBatches(t *testing.T) {
	l := NewQueryLog(filepath.Join("testdata", "querylog.json"))
	for _, limit := range []int{1, 2, 3} {
		got, _ := drain(t, l, "", limit)
		equal(t, got, want)
	}
}

func TestRotationAndAppend(t *testing.T) {
	path := copyTestdata(t)
	l := NewQueryLog(path)
	ctx := context.Background()

	// Stop in the middle of the three identical timestamps.
	qs, cur, err := l.FetchDNS(ctx, "", 6)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, summary(qs), want[:6])
	if cur != "2026-10-02T00:00:01.5+02:00|1" {
		t.Fatalf("cursor = %q", cur)
	}

	// AdGuard finishes the partial line, rotates, and keeps logging.
	appendFile(t, path, `mple.org","QT":"A","IP":"192.168.1.20","Result":{}}`+"\n")
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	appendFile(t, path,
		`{"IP":"192.168.1.40","T":"2026-10-02T00:00:01.5+02:00","QH":"late.ui.com","QT":"A","Result":{}}`+"\n"+
			`{"IP":"192.168.1.50","T":"2026-10-02T00:05:00+02:00","QH":"api.amazonalexa.com","QT":"A","Result":{}}`+"\n")

	got, _ := drain(t, l, cur, 2)
	equal(t, got, append(want[6:],
		"partial.example.org A 192.168.1.20 false",
		"late.ui.com A 192.168.1.40 false",
		"api.amazonalexa.com A 192.168.1.50 false",
	))
}

func TestCursorEntryGone(t *testing.T) {
	path := copyTestdata(t)
	// The user cleared the log; only newer entries remain.
	os.Remove(path + ".1")
	if err := os.WriteFile(path, []byte(
		`{"IP":"192.168.1.20","T":"2026-10-02T00:00:00+02:00","QH":"old.example","QT":"A","Result":{}}`+"\n"+
			`{"IP":"192.168.1.20","T":"2026-10-02T09:00:00+02:00","QH":"new.example","QT":"A","Result":{}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, cur := drain(t, NewQueryLog(path), "2026-10-02T08:00:00+02:00|1", 10)
	equal(t, got, []string{"new.example A 192.168.1.20 false"})
	if cur != "2026-10-02T09:00:00+02:00|1" {
		t.Fatalf("cursor = %q", cur)
	}
}

// TestClockJump covers a host clock that was far ahead and then corrected:
// entries after the jump reuse instants seen before it, and once the
// future-stamped cursor entry rotates away nothing left is stamped after it.
func TestClockJump(t *testing.T) {
	path := filepath.Join(t.TempDir(), "querylog.json")
	line := func(ts, host string) string {
		return `{"IP":"192.168.1.20","T":"` + ts + `","QH":"` + host + `","QT":"A","Result":{}}` + "\n"
	}
	appendFile(t, path, line("2026-10-02T10:00:00Z", "a.example")+
		line("2068-10-02T10:00:00Z", "future.example")+
		line("2026-10-02T10:00:00Z", "b.example"))
	l := NewQueryLog(path)
	got, cur := drain(t, l, "", 1)
	equal(t, got, []string{
		"a.example A 192.168.1.20 false",
		"future.example A 192.168.1.20 false",
		"b.example A 192.168.1.20 false",
	})
	if cur != "2026-10-02T10:00:00Z|2" {
		t.Fatalf("cursor = %q", cur)
	}

	// Two rotations later only entries stamped before the cursor are left.
	if err := os.WriteFile(path, []byte(line("2026-10-03T09:00:00Z", "c.example")), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ = drain(t, l, "2068-10-02T10:00:00Z|1", 10)
	equal(t, got, []string{"c.example A 192.168.1.20 false"})
}

// TestClockJumpLargeLog checks that a backward clock jump in a log big
// enough for the binary search does not lose the cursor's entry.
func TestClockJumpLargeLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "querylog.json")
	var b strings.Builder
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	const n = 4000
	for i := range n {
		ts := base.Add(time.Duration(i) * time.Second)
		if i >= n/2 {
			ts = ts.Add(-24 * time.Hour) // NTP set the clock back a day
		}
		fmt.Fprintf(&b, `{"IP":"10.0.0.1","T":%q,"QH":"host%d.example","QT":"A","Result":{}}`+"\n", ts.Format(time.RFC3339Nano), i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := drain(t, NewQueryLog(path), "", 333)
	if len(got) != n {
		t.Fatalf("read %d entries, want %d", len(got), n)
	}
	for i, s := range got {
		if !strings.HasPrefix(s, fmt.Sprintf("host%d.example ", i)) {
			t.Fatalf("entry %d = %q", i, s)
		}
	}
}

// TestLargeLog exercises the binary search that skips already-read parts
// of a long log.
func TestLargeLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "querylog.json")
	var b strings.Builder
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	const n = 5000
	for i := range n {
		// Every tenth entry was slow: written after entries stamped later.
		ts := base.Add(time.Duration(i) * time.Second)
		if i%10 == 9 {
			ts = ts.Add(-1300 * time.Millisecond)
		}
		fmt.Fprintf(&b, `{"IP":"10.0.0.%d","T":%q,"QH":"host%d.example","QT":"A","Result":{}}`+"\n",
			i%250+1, ts.Format(time.RFC3339Nano), i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := drain(t, NewQueryLog(path), "", 97)
	if len(got) != n {
		t.Fatalf("read %d entries, want %d", len(got), n)
	}
	for i, s := range got {
		if !strings.HasPrefix(s, fmt.Sprintf("host%d.example ", i)) {
			t.Fatalf("entry %d = %q", i, s)
		}
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, _ := f.Stat()
	target := base.Add(4000 * time.Second)
	off := seek(f, fi.Size(), target)
	if off == 0 {
		t.Fatal("seek did not skip ahead")
	}
	if !strings.Contains(b.String()[off:], `"host4000.example"`) {
		t.Fatalf("seek to %d skipped entries at or after %v", off, target)
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	l := NewQueryLog(filepath.Join("testdata", "querylog.json"), WithName("adguard"))
	for _, c := range []string{"nope", "2026-10-02T00:00:03+02:00", "2026-10-02T00:00:03+02:00|0"} {
		if _, _, err := l.FetchDNS(ctx, c, 10); err == nil {
			t.Errorf("cursor %q: expected error", c)
		}
	}
	if _, _, err := NewQueryLog(filepath.Join(t.TempDir(), "querylog.json")).FetchDNS(ctx, "", 10); err == nil {
		t.Error("missing log: expected error")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := l.FetchDNS(cctx, "", 10); err == nil {
		t.Error("cancelled context: expected error")
	}
	if qs, cur, err := l.FetchDNS(ctx, "", 0); qs != nil || cur != "" || err != nil {
		t.Errorf("limit 0: got %v, %q, %v", qs, cur, err)
	}
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

// Names a client sent with control characters are dropped, and a line
// longer than source.MaxLine is skipped without losing the lines around it,
// including while AdGuard Home is still writing it.
func TestHostileLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "querylog.json")
	// Valid but for the padding, so only the length cap skips it.
	long := `{"IP":"192.168.1.5","T":"2026-10-02T00:00:02Z","QH":"long.example","QT":"A","Result":{}}` + strings.Repeat(" ", source.MaxLine)
	appendFile(t, path,
		`{"IP":"192.168.1.5","T":"2026-10-02T00:00:01Z","QH":"one.example","QT":"A","Result":{}}`+"\n"+
			long+"\n"+
			`{"IP":"192.168.1.5","T":"2026-10-02T00:00:03Z","QH":"\u001b]0;owned\u0007\u001b[2Jevil.example","QT":"A","Result":{}}`+"\n"+
			`{"IP":"192.168.1.5","T":"2026-10-02T00:00:04Z","QH":"two.example","QT":"A","Result":{}}`+"\n")
	l := NewQueryLog(path)
	got, cur := drain(t, l, "", 1)
	equal(t, got, []string{"one.example A 192.168.1.5 false", "two.example A 192.168.1.5 false"})

	appendFile(t, path, long)
	got, cur = drain(t, l, cur, 100)
	equal(t, got, nil)
	appendFile(t, path, "\n"+`{"IP":"192.168.1.5","T":"2026-10-02T00:00:05Z","QH":"three.example","QT":"A","Result":{}}`+"\n")
	got, _ = drain(t, l, cur, 100)
	equal(t, got, []string{"three.example A 192.168.1.5 false"})
}
