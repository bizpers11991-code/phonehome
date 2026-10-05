package web

import (
	"net/url"
	"strings"
	"unicode"

	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/redact"
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
		d := redact.Domain(u.Domain, r.Device)
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
