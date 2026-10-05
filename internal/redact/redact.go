// Package redact keeps names that identify a household out of what phonehome
// lets people share: the "Suggest a rule" link and the Privacy Receipt.
package redact

import (
	"strings"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// localSuffixes are names that only mean something inside one network, plus
// reverse-lookup zones. No knowledge base rule can describe them, and they
// tend to carry hostnames and addresses.
var localSuffixes = []string{
	"arpa", // in-addr.arpa, ip6.arpa, home.arpa, resolver.arpa
	"local", "lan", "home", "internal", "intranet", "corp", "private",
	"localdomain", "localhost", "test", "invalid",
}

// Local reports whether domain is a local name or in a reverse-lookup zone.
func Local(domain string) bool {
	for _, s := range localSuffixes {
		if domain == s || strings.HasSuffix(domain, "."+s) {
			return true
		}
	}
	return false
}

// Domain returns domain as it may be shown outside the house, or "" if it
// should not be shown at all. ID-like labels (serial numbers, account IDs,
// embedded addresses) become "*", and a run of them folds into one. Invalid
// names, local names and reverse lookups (Local), names that mention device d
// (see mentionsDevice) and names with fewer than two labels left after
// redaction are dropped.
func Domain(domain string, d model.Device) string {
	domain = strings.ToLower(domain)
	labels := strings.Split(domain, ".")
	if len(domain) > 253 || len(labels) < 2 {
		return ""
	}
	for _, lb := range labels {
		if lb == "" || len(lb) > 63 || strings.Trim(lb, "abcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
			return ""
		}
	}
	if Local(domain) || mentionsDevice(domain, d) {
		return ""
	}
	out := make([]string, 0, len(labels))
	for _, lb := range labels {
		if idLike(lb) {
			lb = "*"
		}
		if lb == "*" && len(out) > 0 && out[len(out)-1] == "*" {
			continue
		}
		out = append(out, lb)
	}
	named := 0
	for _, lb := range out {
		if lb != "*" {
			named++
		}
	}
	if named < 2 {
		return ""
	}
	return strings.Join(out, ".")
}

// mentionsDevice reports whether domain starts with the device's hostname or
// label (as when a search domain is appended to a local name) or contains its
// MAC (with colons, dashes, Cisco-style dots or no separators) or one of its
// addresses.
func mentionsDevice(domain string, d model.Device) bool {
	host, _, _ := strings.Cut(strings.ToLower(d.Hostname), ".")
	label := strings.Join(strings.Fields(strings.ToLower(d.Label)), "-")
	for _, n := range []string{host, label} {
		if n != "" && strings.HasPrefix(domain, n+".") {
			return true
		}
	}
	var needles []string
	if d.MAC != "" {
		mac := strings.ToLower(d.MAC)
		bare := strings.NewReplacer(":", "", "-", "", ".", "").Replace(mac)
		needles = append(needles, mac, bare, strings.ReplaceAll(mac, ":", "-"))
		if len(bare) == 12 {
			needles = append(needles, bare[:4]+"."+bare[4:8]+"."+bare[8:])
		}
	}
	for _, ip := range d.IPs {
		s := ip.String()
		needles = append(needles, s, strings.NewReplacer(".", "-", ":", "-").Replace(s))
	}
	for _, n := range needles {
		if strings.Contains(domain, n) {
			return true
		}
	}
	return false
}

// idLike reports whether a DNS label looks like an identifier rather than a
// name: all digits (often part of an embedded address), a long hex string or
// UUID, or a long label that is mostly digits.
func idLike(lb string) bool {
	digits, hex := 0, true
	for _, r := range lb {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r >= 'a' && r <= 'f', r == '-':
		default:
			hex = false
		}
	}
	switch {
	case digits == len(lb):
		return true
	case hex && len(lb) >= 8 && digits >= 2:
		return true
	case len(lb) >= 16 && digits*4 >= len(lb):
		return true
	}
	return false
}
