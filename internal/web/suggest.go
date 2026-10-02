package web

import (
	"net/url"
	"strings"
	"unicode"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// newIssueURL is the knowledge-base report form, .github/ISSUE_TEMPLATE/new-device.yml.
const newIssueURL = "https://github.com/bizpers11991-code/phonehome/issues/new"

// maxSuggest caps the domains put into one issue link (GitHub truncates long URLs).
const maxSuggest = 20

// suggestURL builds the "Suggest a rule" link for a device: GitHub's
// new-issue form for the new-device template, prefilled with the device's
// vendor and kind and its unclassified domain names. Nothing else goes in: no
// addresses, MACs, hostnames or labels. ID-like labels inside domain names
// (serial numbers, account IDs, embedded addresses) become "*", and names
// that contain the device's own hostname are left out.
//
// phonehome never requests this URL itself; the person clicks it, and the
// dashboard says that GitHub then sees these names. Returns "" when there is
// nothing to suggest.
func suggestURL(r model.DeviceReport) string {
	var doms []string
	seen := map[string]bool{}
	for _, u := range r.Unknown {
		d := shareable(u.Domain, r.Device)
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		doms = append(doms, d)
		if len(doms) == maxSuggest {
			break
		}
	}
	if len(doms) == 0 {
		return ""
	}
	device := deviceWords(r.Device)
	title := "Unclassified domains"
	if device != "" {
		title += ": " + device
	}
	q := url.Values{}
	q.Set("template", "new-device.yml")
	q.Set("title", title)
	q.Set("device", device)
	q.Set("domains", strings.Join(doms, "\n"))
	return newIssueURL + "?" + q.Encode()
}

// deviceWords describes a device by vendor and kind only, e.g. "Samsung tv".
func deviceWords(d model.Device) string {
	vendor := strings.Join(strings.FieldsFunc(d.Vendor, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}), " ")
	if len(vendor) > 60 {
		vendor = vendor[:60]
	}
	var parts []string
	if vendor != "" {
		parts = append(parts, vendor)
	}
	if d.Kind != "" && d.Kind != model.KindUnknown {
		parts = append(parts, string(d.Kind))
	}
	return strings.Join(parts, " ")
}

// shareable returns domain as it may appear in a public issue, or "" if it
// should not be shared at all.
func shareable(domain string, d model.Device) string {
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
	if mentionsDevice(domain, d) {
		return ""
	}
	// Redact ID-like labels and fold a leading run of them into one "*".
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
// MAC or one of its addresses.
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
		needles = append(needles, mac, strings.ReplaceAll(mac, ":", ""), strings.ReplaceAll(mac, ":", "-"))
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
