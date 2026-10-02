package leases

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/oui"
)

// parseDhcpd parses an ISC dhcpd lease database (dhcpd.leases(5)). dhcpd
// appends a new block whenever a lease changes, so for each address only the
// last block counts. Leases without a hardware address (abandoned or never
// used) are skipped, as are lease6 and host declarations.
func parseDhcpd(data []byte) ([]lease, error) {
	toks, err := tokenize(string(data))
	if err != nil {
		return nil, err
	}
	var (
		order  []netip.Addr
		byAddr = map[netip.Addr]lease{}
	)
	for len(toks) > 0 {
		// Top level: either "<keyword> ... ;" or "<keyword> ... { body }".
		end := 0
		for end < len(toks) && toks[end] != ";" && toks[end] != "{" {
			end++
		}
		if end == len(toks) {
			return nil, errors.New("dhcpd lease file: unterminated statement")
		}
		head := toks[:end]
		if toks[end] == ";" {
			toks = toks[end+1:]
			continue
		}
		body, rest, err := block(toks[end+1:])
		if err != nil {
			return nil, err
		}
		toks = rest
		if len(head) != 2 || head[0] != "lease" {
			continue
		}
		ip, err := netip.ParseAddr(head[1])
		if err != nil || !ip.Is4() {
			return nil, fmt.Errorf("dhcpd lease file: bad lease address %q", head[1])
		}
		l, ok := dhcpdLease(ip, body)
		if _, seen := byAddr[ip]; !seen {
			order = append(order, ip)
		}
		if ok {
			byAddr[ip] = l
		} else {
			// A later block without a MAC still replaces an earlier one:
			// the address no longer belongs to that device.
			byAddr[ip] = lease{}
		}
	}
	var out []lease
	for _, ip := range order {
		if l := byAddr[ip]; l.MAC != "" {
			out = append(out, l)
		}
	}
	return out, nil
}

// block splits toks, which start just after a "{", into the statements of
// that block and the tokens after its closing "}". Nested blocks (such as
// "on commit { ... }") are kept intact in the body.
func block(toks []string) (body, rest []string, err error) {
	depth := 1
	for i, t := range toks {
		switch t {
		case "{":
			depth++
		case "}":
			depth--
			if depth == 0 {
				return toks[:i], toks[i+1:], nil
			}
		}
	}
	return nil, nil, errors.New("dhcpd lease file: unterminated block")
}

// dhcpdLease extracts the fields phonehome uses from a lease block body.
func dhcpdLease(ip netip.Addr, body []string) (lease, bool) {
	l := lease{IP: ip}
	var starts, cltt time.Time
	for len(body) > 0 {
		end := 0
		for end < len(body) && body[end] != ";" && body[end] != "{" {
			end++
		}
		st := body[:end]
		if end < len(body) && body[end] == "{" {
			_, rest, err := block(body[end+1:])
			if err != nil {
				return lease{}, false
			}
			body = rest
		} else {
			body = body[min(end+1, len(body)):]
		}
		if len(st) == 0 {
			continue
		}
		switch {
		case len(st) == 3 && st[0] == "hardware" && st[1] == "ethernet":
			l.MAC, _ = oui.Normalize(st[2])
		case len(st) == 2 && st[0] == "client-hostname":
			l.Hostname = strings.TrimPrefix(st[1], `"`)
		case st[0] == "starts":
			starts = dhcpdTime(st[1:])
		case st[0] == "cltt":
			cltt = dhcpdTime(st[1:])
		}
	}
	l.Seen = cltt
	if l.Seen.IsZero() {
		l.Seen = starts
	}
	return l, l.MAC != ""
}

// dhcpdTime parses the date of a starts/ends/cltt statement: either
// "<weekday> YYYY/MM/DD HH:MM:SS" in UTC, or "epoch <unix seconds>" when
// dhcpd runs with db-time-format local. "never" and junk give the zero time.
func dhcpdTime(f []string) time.Time {
	if len(f) == 2 && f[0] == "epoch" {
		if sec, err := strconv.ParseInt(f[1], 10, 64); err == nil {
			return time.Unix(sec, 0).UTC()
		}
		return time.Time{}
	}
	if len(f) == 3 {
		if t, err := time.Parse("2006/01/02 15:04:05", f[1]+" "+f[2]); err == nil {
			return t
		}
	}
	return time.Time{}
}

// tokenize splits a dhcpd.leases file into words, decoded quoted strings,
// and the punctuation ";", "{" and "}". Comments run from "#" to end of line.
// A quoted string keeps its opening '"' so that one which decodes to ";",
// "{" or "}" (a client may send "}" as its host name) is never taken for
// punctuation; words cannot start with '"'.
func tokenize(s string) ([]string, error) {
	var toks []string
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '#':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == ';' || c == '{' || c == '}':
			toks = append(toks, s[i:i+1])
			i++
		case c == '"':
			str, n, err := quoted(s[i:])
			if err != nil {
				return nil, err
			}
			toks = append(toks, `"`+str)
			i += n
		default:
			j := i
			for j < len(s) && !strings.ContainsRune(" \t\r\n;{}\"#", rune(s[j])) {
				j++
			}
			toks = append(toks, s[i:j])
			i = j
		}
	}
	return toks, nil
}

// quoted decodes the dhcpd string literal at the start of s and returns it
// with the number of bytes consumed. dhcpd escapes non-printable bytes as
// three-digit octal ("\303\251" is "é") and '"' and '\' with a backslash.
func quoted(s string) (string, int, error) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			return b.String(), i + 1, nil
		case '\\':
			if i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
				n, _ := strconv.ParseUint(s[i+1:i+4], 8, 16)
				b.WriteByte(byte(n))
				i += 3
			} else if i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", 0, errors.New("dhcpd lease file: unterminated string")
}

func isOctal(c byte) bool { return '0' <= c && c <= '7' }
