// Package pihole reads DNS lookups and devices from Pi-hole.
//
// Two readers are provided: DB reads FTL's long-term database
// (pihole-FTL.db) directly and also lists devices from its network table;
// API talks to the Pi-hole v6 REST API, for when phonehome runs on another
// machine. Both are strictly read-only.
//
// The numeric query types and statuses below mirror enum query_type and
// enum query_status in FTL's src/enums.h, and the string forms mirror
// get_query_type_str / get_query_status_str used by the v6 API.
package pihole

import (
	"fmt"
	"math"
	"net/netip"
	"strings"
	"time"
)

// Option configures a DB or API reader.
type Option func(*options)

type options struct {
	name string
}

// WithName overrides the source name (default "pihole" for DB and
// "pihole-api" for API). Use it to tell several Pi-holes apart: the name is
// what cursors are stored under.
func WithName(name string) Option {
	return func(o *options) { o.name = name }
}

func applyOptions(defaultName string, opts []Option) options {
	o := options{name: defaultName}
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// queryTypes maps FTL's query type numbers to record type names. Index 14
// is TYPE_OTHER, which older FTL versions stored without saying which type
// it was, so it maps to "" (unknown).
var queryTypes = [...]string{
	1: "A", 2: "AAAA", 3: "ANY", 4: "SRV", 5: "SOA", 6: "PTR", 7: "TXT",
	8: "NAPTR", 9: "MX", 10: "DS", 11: "RRSIG", 12: "DNSKEY", 13: "NS",
	14: "", 15: "SVCB", 16: "HTTPS",
}

// queryTypeName converts a type number from the database. FTL stores types
// it has no constant for as 100 + the RR type number; those are rendered
// the way FTL and RFC 3597 do, e.g. "TYPE65".
func queryTypeName(t int64) string {
	switch {
	case t > 100:
		return fmt.Sprintf("TYPE%d", t-100)
	case t > 0 && t < int64(len(queryTypes)):
		return queryTypes[t]
	}
	return ""
}

// apiTypeName converts a type string from the API, which already uses
// record type names ("A", "HTTPS", "TYPE65"). get_query_type_str says
// "NONE" for type 0 and "N/A" for values it does not know.
func apiTypeName(s string) string {
	switch s {
	case "OTHER", "UNKNOWN", "NONE", "N/A":
		return ""
	}
	return s
}

// statuses lists FTL's query statuses in enum order and whether each one
// means Pi-hole (or its upstream) refused to resolve the domain. It matches
// the "blocked" set in Pi-hole's query database documentation.
var statuses = [...]struct {
	name    string
	blocked bool
}{
	{"UNKNOWN", false},
	{"GRAVITY", true},
	{"FORWARDED", false},
	{"CACHE", false},
	{"REGEX", true},
	{"DENYLIST", true},
	{"EXTERNAL_BLOCKED_IP", true},
	{"EXTERNAL_BLOCKED_NULL", true},
	{"EXTERNAL_BLOCKED_NXRA", true},
	{"GRAVITY_CNAME", true},
	{"REGEX_CNAME", true},
	{"DENYLIST_CNAME", true},
	{"RETRIED", false},
	{"RETRIED_DNSSEC", false},
	{"IN_PROGRESS", false},
	{"DBBUSY", true}, // blocked because gravity was busy
	{"SPECIAL_DOMAIN", true},
	{"CACHE_STALE", false},
	{"EXTERNAL_BLOCKED_EDE15", true},
}

// statusBlocked reports whether a database status number means blocked.
func statusBlocked(s int64) bool {
	return s >= 0 && s < int64(len(statuses)) && statuses[s].blocked
}

// apiStatusBlocked reports whether an API status string means blocked.
// BLACKLIST and BLACKLIST_CNAME are the names early v6 builds used.
func apiStatusBlocked(s string) bool {
	switch s {
	case "BLACKLIST", "BLACKLIST_CNAME":
		return true
	}
	for _, st := range statuses {
		if st.name == s {
			return st.blocked
		}
	}
	return false
}

func normalizeDomain(d string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(d), "."))
}

// parseAddr parses an IP address as FTL prints it, dropping any zone and
// unmapping IPv4-in-IPv6 so the same client always compares equal.
func parseAddr(s string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return netip.Addr{}, false
	}
	return a.WithZone("").Unmap(), true
}

// unixTime converts FTL's timestamps, which are whole seconds in Pi-hole v5
// and fractional seconds in v6, rounded to the microsecond FTL records.
func unixTime(ts float64) time.Time {
	if ts <= 0 {
		return time.Time{}
	}
	sec, frac := math.Modf(ts)
	return time.Unix(int64(sec), int64(frac*1e9)).Round(time.Microsecond)
}
