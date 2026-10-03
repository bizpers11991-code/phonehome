package dnsmasq

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/source"
)

var _ source.DNSSource = (*Log)(nil)

// testNow is when the tests pretend to run; syslog lines carry no year.
var testNow = time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC)

func newTestLog(path string) *Log {
	l := NewLog(path, WithLocation(time.UTC))
	l.now = func() time.Time { return testNow }
	return l
}

var wantSyslog = []string{
	"13:58:01 api.amazonalexa.com A 192.168.1.30 false",
	"13:58:02 samsungacr.com A 192.168.1.20 true",
	"13:58:03 samsungacr.com AAAA 192.168.1.20 true",
	"13:58:05 time.samsungcloudsolution.com HTTPS fe80::aabb:ccff:fe00:1122 false",
	"13:58:06 tracking.example.net A 192.168.1.40 true",
	"13:58:07 20.1.168.192.in-addr.arpa PTR 127.0.0.1 false",
}

func summary(t *testing.T, qs []model.DNSQuery) []string {
	t.Helper()
	var out []string
	for _, q := range qs {
		if q.Source != "dnsmasq" {
			t.Fatalf("Source = %q", q.Source)
		}
		out = append(out, fmt.Sprintf("%s %s %s %s %t",
			q.Time.UTC().Format("15:04:05.999999"), q.Domain, q.QType, q.ClientIP, q.Blocked))
	}
	return out
}

func equal(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// drain reads everything with the given batch size.
func drain(t *testing.T, l *Log, cursor string, limit int) ([]string, string) {
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
		all = append(all, summary(t, qs)...)
		cursor = next
	}
	t.Fatal("cursor never settled")
	return nil, ""
}

func TestFormats(t *testing.T) {
	for file, want := range map[string][]string{
		"syslog.log": wantSyslog,
		"extra.log": {
			"13:59:00 example.org A 192.168.1.20 false",
			"13:59:00 example.org A 192.168.1.21 false",
			"13:59:01 2.2.1.1.0.0.e.f.f.f.c.c.b.b.a.a.0.0.0.0.0.0.0.0.0.0.0.0.0.0.d.f.ip6.arpa PTR fd00::20 false",
			"13:59:02 doubleclick.net A 192.168.1.50 true",
			"13:59:03 mask.icloud.com HTTPS 192.168.1.51 true",
			"13:59:04 metrics.roku.com A 192.168.1.52 true",
		},
		"ede.log": { // answers carrying extended DNS errors
			"14:20:00 ads.example.com A 192.168.1.20 true",
			"14:20:01 stale.example.com A 192.168.1.21 false",
			"14:20:02 early.example.com AAAA 192.168.1.22 false",
		},
		"mixed.log": {
			"12:01:00.123456 connectivitycheck.gstatic.com A 192.168.1.60 false",
			"14:02:00 device-metrics-us.amazon.com A 192.168.1.61 true",
		},
		// Pi-hole v6's pihole.log with misc.extraLogging, which writes
		// log-queries=proto: "UDP <serial> <requestor>/<port> ...".
		"pihole-v6-proto.log": {
			"13:59:00 example.org A 192.168.1.20 false",
			"13:59:01 example.org AAAA fd00::20 false",
			"13:59:02 metrics.vendor.example A 192.168.1.21 true", // CNAME-blocked
			"13:59:02 www.cdn-site.example HTTPS 192.168.1.22 false",
			"13:59:03 ads.example.net A 192.168.1.23 true",
			"13:59:03 deny.example A 192.168.1.23 true",
			"13:59:03 tracker.metrics.example AAAA 192.168.1.23 true",
			"13:59:04 use-application-dns.net A 192.168.1.24 true",
			"13:59:04 flood.example A 192.168.1.25 true", // rate-limited
			"13:59:05 upstream-blocked.example A 192.168.1.26 true",
			"13:59:05 pi.hole A 192.168.1.26 false",
			"13:59:06 slow.example A 192.168.1.27 false",
			"13:59:06 busy.example A 192.168.1.27 true",
			"13:59:08 signed.vendor.example A 192.168.1.28 true", // DNSSEC-validated, then CNAME-blocked
		},
		// Pi-hole v5's pihole.log: plain log-queries, v5's reason names.
		"pihole-v5.log": {
			"14:30:00 metrics.vendor.example A 192.168.1.21 true", // CNAME-blocked
			"14:30:01 www.cdn-site.example A 192.168.1.22 false",
			"14:30:02 deny.example A 192.168.1.23 true",
			"14:30:02 ads.tracker.example A 192.168.1.23 true",
			"14:30:03 mask.icloud.com A 192.168.1.24 true",
			"14:30:03 cached.example A 192.168.1.24 false",
		},
	} {
		t.Run(file, func(t *testing.T) {
			path := filepath.Join("testdata", file)
			qs, cur, err := newTestLog(path).FetchDNS(context.Background(), "", 100)
			if err != nil {
				t.Fatal(err)
			}
			equal(t, summary(t, qs), want)
			fi, _ := os.Stat(path)
			if want := fmt.Sprintf("%d:%d", inode(fi), fi.Size()); cur != want {
				t.Errorf("cursor = %q, want %q", cur, want)
			}
			if qs[0].Time.Year() != 2026 {
				t.Errorf("year = %d", qs[0].Time.Year())
			}
		})
	}
}

func TestSmallBatches(t *testing.T) {
	l := newTestLog(filepath.Join("testdata", "syslog.log"))
	for _, limit := range []int{1, 2, 4} {
		got, _ := drain(t, l, "", limit)
		equal(t, got, wantSyslog)
	}
}

// TestHoldBack checks that a query is not reported before its answer is
// logged, since the answer decides whether it was blocked.
func TestHoldBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dnsmasq.log")
	l := newTestLog(path)
	appendFile(t, path, "Oct  2 14:10:00 dnsmasq[9]: query[A] ads.example.com from 192.168.1.20\n")
	got, cur := drain(t, l, "", 10)
	equal(t, got, nil)

	appendFile(t, path, "Oct  2 14:10:00 dnsmasq[9]: config ads.example.com is 0.0.0.0\n"+
		"Oct  2 14:10:01 dnsmasq[9]: query[A] half.example.com fr")
	got, cur = drain(t, l, cur, 10)
	equal(t, got, []string{"14:10:00 ads.example.com A 192.168.1.20 true"})

	// Forwarding settles nothing: Pi-hole may block on the reply.
	appendFile(t, path, "om 192.168.1.20\nOct  2 14:10:01 dnsmasq[9]: forwarded half.example.com to 9.9.9.9\n")
	got, cur = drain(t, l, cur, 10)
	equal(t, got, nil)

	appendFile(t, path, "Oct  2 14:10:01 dnsmasq[9]: reply half.example.com is 198.51.100.1\n")
	got, _ = drain(t, l, cur, 10)
	equal(t, got, []string{"14:10:01 half.example.com A 192.168.1.20 false"})
}

func TestTruncation(t *testing.T) {
	path := copyFile(t, "syslog.log")
	l := newTestLog(path)
	_, cur := drain(t, l, "", 100)

	// copytruncate: same inode, smaller file.
	b, err := os.ReadFile(filepath.Join("testdata", "mixed.log"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := drain(t, l, cur, 100)
	equal(t, got, []string{
		"12:01:00.123456 connectivitycheck.gstatic.com A 192.168.1.60 false",
		"14:02:00 device-metrics-us.amazon.com A 192.168.1.61 true",
	})
}

func TestRotation(t *testing.T) {
	path := copyFile(t, "syslog.log")
	l := newTestLog(path)
	qs, cur, err := l.FetchDNS(context.Background(), "", 2)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, summary(t, qs), wantSyslog[:2])

	// dnsmasq writes a little more, logrotate moves the file aside, and
	// dnsmasq reopens a fresh one.
	appendFile(t, path, "Oct  2 13:59:30 router dnsmasq[812]: query[A] last.example from 192.168.1.30\n"+
		"Oct  2 13:59:30 router dnsmasq[812]: cached last.example is 1.2.3.4\n")
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	appendFile(t, path, "Oct  2 14:00:00 router dnsmasq[812]: query[A] first.example from 192.168.1.30\n"+
		"Oct  2 14:00:00 router dnsmasq[812]: config first.example is NXDOMAIN\n")

	got, _ := drain(t, l, cur, 3)
	equal(t, got, slices.Concat(wantSyslog[2:], []string{
		"13:59:30 last.example A 192.168.1.30 false",
		"14:00:00 first.example A 192.168.1.30 true",
	}))
}

func TestParseTimeYear(t *testing.T) {
	newYear := time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		header string
		now    time.Time
		want   time.Time
	}{
		{"Dec 31 23:59:59 host", newYear, time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC)},
		{"Jan  1 00:10:00", newYear, time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC)},
		{"Oct  3 10:00:00", testNow, time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)},
		{"Oct  4 10:00:00", testNow, time.Date(2025, 10, 4, 10, 0, 0, 0, time.UTC)},
		{"Thu Jan  1 00:10:00 2026 daemon.info", newYear, time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC)},
		{"2026-01-01T01:10:00+01:00 host", newYear, time.Date(2026, 1, 1, 0, 10, 0, 0, time.UTC)},
	} {
		got, ok := parseTime(tc.header, time.UTC, tc.now)
		if !ok || !got.Equal(tc.want) {
			t.Errorf("parseTime(%q) = %v, %t; want %v", tc.header, got, ok, tc.want)
		}
	}
	if _, ok := parseTime("garbage", time.UTC, testNow); ok {
		t.Error("parseTime(garbage) succeeded")
	}
}

func TestLocation(t *testing.T) {
	berlin := time.FixedZone("CEST", 2*3600)
	l := NewLog(filepath.Join("testdata", "syslog.log"), WithLocation(berlin), WithName("router"))
	l.now = func() time.Time { return testNow }
	qs, _, err := l.FetchDNS(context.Background(), "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 2, 11, 58, 1, 0, time.UTC); !qs[0].Time.Equal(want) || qs[0].Source != "router" {
		t.Fatalf("got %v from %q, want %v from router", qs[0].Time, qs[0].Source, want)
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	l := newTestLog(filepath.Join("testdata", "syslog.log"))
	for _, c := range []string{"12", "a:b", "1:-5"} {
		if _, _, err := l.FetchDNS(ctx, c, 10); err == nil {
			t.Errorf("cursor %q: expected error", c)
		}
	}
	if _, _, err := newTestLog(filepath.Join(t.TempDir(), "nope.log")).FetchDNS(ctx, "", 10); err == nil {
		t.Error("missing file: expected error")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := l.FetchDNS(cctx, "", 10); err == nil {
		t.Error("cancelled context: expected error")
	}
}

func copyFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
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

// TestParseMessage checks single lines as the reader passes them, newline
// included, so a parsing mistake cannot hide behind the line after it.
func TestParseMessage(t *testing.T) {
	for _, tc := range []struct {
		msg  string
		want string // "serial name blocked cname", or "" when not parsed
	}{
		{"UDP 12 192.168.1.20/5353 query[A] a.example from 192.168.1.20\n", "12 a.example false false"},
		{"TCP 13 fd00::20/40001 query[AAAA] a.example from fd00::20\n", "13 a.example false false"},
		{"12 192.168.1.20/5353 reply a.example is 192.0.2.1 (DNSSEC signed)\n", "12 a.example false false"},
		{"reply a.example is <CNAME>\n", " a.example false true"},
		{"reply tracker.example is blocked during CNAME inspection\n", " tracker.example true false"},
		{"gravity blocked (CNAME) a.example is 0.0.0.0\n", " a.example true false"},
		{"Rate-limiting a.example is REFUSED (EDE: blocked)\n", " a.example true false"},
		{"Pi-hole hostname pi.hole is 192.168.1.2\n", " pi.hole false false"},
		{"config a.example is REFUSED (EDE: not ready)\n", " a.example false false"},
		{"12 192.168.1.20/5353 validation a.example is SECURE\n", ""},
		{"forwarded a.example to 127.0.0.1#5335\n", ""},
		{"reply is truncated\n", ""},
		{"214 dnssec-query[DS] example to 127.0.0.1#5335\n", ""},
	} {
		e, ok := parseMessage(tc.msg)
		got := ""
		if ok {
			got = fmt.Sprintf("%s %s %t %t", e.serial, e.name, e.blocked, e.cname)
		}
		if got != tc.want {
			t.Errorf("parseMessage(%q) = %q, want %q", tc.msg, got, tc.want)
		}
	}
}
