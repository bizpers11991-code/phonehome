package dnsmasq

import (
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// event is one parsed dnsmasq log line that matters to us: either a query
// or an answer that settles one (answered, refused, blocked). "forwarded"
// lines settle nothing: Pi-hole may still block a forwarded query when the
// reply arrives (CNAME inspection, a blocking upstream).
type event struct {
	time    time.Time
	serial  string // log-queries=extra id, "" otherwise
	query   bool
	qtype   string // queries only
	client  netip.Addr
	name    string // lowercased, no trailing dot
	key     string // what answers call it: name, or the address a PTR query asks about
	blocked bool   // answers only
	cname   bool   // answers only: "is <CNAME>", more of the chain follows
}

// programs are the syslog tags whose lines we read. Pi-hole's FTL embeds
// dnsmasq and logs the same lines to /var/log/pihole/pihole.log.
var programs = []string{" dnsmasq[", " dnsmasq:", " pihole-FTL[", " pihole-FTL:"}

// parseLine parses one log line. Timestamps without a year are placed in
// now's year, or the year before if that would put them over a day in the
// future (a log read just after New Year).
func parseLine(line string, loc *time.Location, now time.Time) (event, bool) {
	at := -1
	for _, p := range programs {
		if i := strings.Index(line, p); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	if at < 0 {
		return event{}, false
	}
	colon := strings.Index(line[at+1:], ": ")
	if colon < 0 {
		return event{}, false
	}
	ts, ok := parseTime(line[:at], loc, now)
	if !ok {
		return event{}, false
	}
	e, ok := parseMessage(line[at+1+colon+2:])
	e.time = ts
	return e, ok
}

// parseTime understands the timestamp formats dnsmasq lines come with:
//
//	Oct  2 13:58:01 [host]                     classic syslog, log-facility=<file>
//	2026-10-02T13:58:01.123456+02:00 host       rsyslog high-precision (RFC 3339)
//	Thu Oct  2 13:58:01 2026 daemon.info        OpenWrt logread
func parseTime(header string, loc *time.Location, now time.Time) (time.Time, bool) {
	f := strings.Fields(header)
	switch {
	case len(f) >= 1 && len(f[0]) >= 20 && f[0][4] == '-' && f[0][10] == 'T':
		t, err := time.Parse(time.RFC3339Nano, f[0])
		return t, err == nil
	case len(f) >= 5 && len(f[0]) == 3 && len(f[4]) == 4 && isDigits(f[4]):
		t, err := time.ParseInLocation("Jan 2 15:04:05 2006", strings.Join(f[1:5], " "), loc)
		return t, err == nil
	case len(f) >= 3:
		t, err := time.ParseInLocation("Jan 2 15:04:05", strings.Join(f[:3], " "), loc)
		if err != nil {
			return time.Time{}, false
		}
		n := now.In(loc)
		t = t.AddDate(n.Year(), 0, 0)
		if t.After(n.Add(24 * time.Hour)) {
			t = t.AddDate(-1, 0, 0)
		}
		return t, true
	}
	return time.Time{}, false
}

// parseMessage parses what dnsmasq's log_query() writes:
//
//	query[A] example.com from 192.168.1.20
//	reply example.com is 93.184.216.34
//	config ads.example.com is 0.0.0.0
//
// With log-queries=extra each line starts with a serial number and usually
// the requestor's address and port: "45 192.168.1.20/53210 query[A] ...".
// log-queries=proto (Pi-hole's misc.extraLogging) puts "UDP " or "TCP "
// before the serial. Answers may end in " (DNSSEC signed)" with extra
// logging, or, from dnsmasq 2.86, in an extended DNS error such as
// " (EDE: stale answer)"; both are dropped.
func parseMessage(msg string) (event, bool) {
	if i := strings.Index(msg, " (EDE:"); i >= 0 {
		msg = msg[:i]
	}
	msg = strings.TrimSuffix(strings.TrimSpace(msg), " (DNSSEC signed)")
	var e event
	f := strings.Fields(msg)
	if len(f) > 2 && (f[0] == "UDP" || f[0] == "TCP") && isDigits(f[1]) {
		f = f[1:]
	}
	if len(f) > 1 && isDigits(f[0]) {
		e.serial, f = f[0], f[1:]
		if i := strings.LastIndexByte(f[0], '/'); i > 0 && isDigits(f[0][i+1:]) {
			f = f[1:]
		}
	}
	// Pi-hole blocks a whole CNAME chain when one of its names is on a
	// list: "reply tracker.example is blocked during CNAME inspection".
	if n := len(f); n >= 7 && strings.Join(f[n-5:], " ") == "is blocked during CNAME inspection" {
		f = append(f[:n-5:n-5], "is", "blocked")
	}
	switch n := len(f); {
	case n == 4 && strings.HasPrefix(f[0], "query[") && strings.HasSuffix(f[0], "]") && f[2] == "from":
		client, err := netip.ParseAddr(f[3])
		if err != nil {
			return event{}, false
		}
		e.query = true
		e.qtype = typeName(f[0][len("query[") : len(f[0])-1])
		e.client = client.WithZone("").Unmap()
		e.name = normalize(f[1])
	case n >= 4 && f[n-2] == "is" && f[0] != "validation":
		// source, name, "is", answer; the source may be several words.
		// Lines with no name ("reply is truncated") are not answers, and
		// neither are DNSSEC results ("validation a.example is SECURE"),
		// which dnsmasq logs before it processes the reply.
		e.name = normalize(f[n-3])
		e.blocked = isBlock(strings.Join(f[:n-3], " "), f[n-1])
		e.cname = f[n-1] == "<CNAME>"
	default:
		return event{}, false
	}
	e.key = e.name
	if a, ok := reverseAddr(e.name); ok {
		e.key = a.String()
	} else if a, err := netip.ParseAddr(e.name); err == nil {
		e.key = a.String()
	}
	return e, e.name != ""
}

// reverseAddr decodes a PTR query name (20.1.168.192.in-addr.arpa or the
// ip6.arpa nibble form), since dnsmasq logs the answer under the address.
func reverseAddr(name string) (netip.Addr, bool) {
	if v4, ok := strings.CutSuffix(name, ".in-addr.arpa"); ok {
		l := strings.Split(v4, ".")
		if len(l) != 4 {
			return netip.Addr{}, false
		}
		a, err := netip.ParseAddr(l[3] + "." + l[2] + "." + l[1] + "." + l[0])
		return a, err == nil
	}
	if v6, ok := strings.CutSuffix(name, ".ip6.arpa"); ok {
		l := strings.Split(v6, ".")
		if len(l) != 32 {
			return netip.Addr{}, false
		}
		var b [16]byte
		for i, nib := range l {
			n, err := strconv.ParseUint(nib, 16, 8)
			if err != nil || len(nib) != 1 {
				return netip.Addr{}, false
			}
			b[15-i/2] |= byte(n) << (4 * (i % 2))
		}
		return netip.AddrFrom16(b), true
	}
	return netip.Addr{}, false
}

// isBlock reports whether an answer means the query was refused. dnsmasq
// answers names configured with address=/name/ (or address=/name/#,
// the usual blocklist format) from "config". Pi-hole logs its own verdicts
// with the reason as the source ("gravity blocked", "regex denied", "exactly
// blacklisted", "blocked upstream with NULL address", "special domain",
// "Mozilla canary domain", "Rate-limiting", ...), exactly the cases its
// database stores with a blocked status (and rate-limited queries, which
// it does not store at all).
func isBlock(source, answer string) bool {
	switch source {
	case "config":
		switch answer {
		case "0.0.0.0", "::", "NXDOMAIN", "NODATA", "NODATA-IPv4", "NODATA-IPv6":
			return true
		}
		return false
	case "reply":
		return answer == "blocked" // during CNAME inspection
	case "special domain", "Mozilla canary domain", "Apple iCloud Private Relay domain",
		"Designated Resolver domain", "Rate-limiting":
		return true
	}
	return strings.Contains(source, "blocked") || strings.Contains(source, "blacklisted") ||
		strings.Contains(source, "denied") || strings.HasSuffix(source, "(gravity database is not available)")
}

// typeName turns dnsmasq's "type=65" for types it has no name for into the
// RFC 3597 form other sources use.
func typeName(t string) string {
	if n, ok := strings.CutPrefix(t, "type="); ok {
		switch n {
		case "64":
			return "SVCB"
		case "65":
			return "HTTPS"
		}
		if _, err := strconv.Atoi(n); err == nil {
			return "TYPE" + n
		}
		return ""
	}
	return t
}

func normalize(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, "."))
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
