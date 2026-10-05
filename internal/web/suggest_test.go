package web

import (
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

func TestSuggestURL(t *testing.T) {
	r := model.DeviceReport{
		Device: model.Device{
			ID:       "mac:3c:22:fb:7e:90:aa",
			MAC:      "3c:22:fb:7e:90:aa",
			IPs:      []netip.Addr{netip.MustParseAddr("192.168.1.105"), netip.MustParseAddr("fd00::1:2")},
			Hostname: "Dans-MacBook-Air.lan",
			Label:    "Dan's laptop",
			Vendor:   "Apple,\n Inc.",
			Kind:     model.KindComputer,
		},
		Unknown: []model.UnknownDomain{
			{Domain: "cdn.vendor.example", Count: 90},
			{Domain: "dans-macbook-air.corp.example", Count: 50},       // search domain appended to the hostname
			{Domain: "dan's-laptop.example", Count: 40},                // not a valid name
			{Domain: "105.1.168.192.dnsbl.example", Count: 30},         // embedded address, reversed
			{Domain: "host-192-168-1-105.isp.example", Count: 25},      // embedded address, dashed
			{Domain: "3c22fb7e90aa.tracker.example", Count: 20},        // embedded MAC
			{Domain: "a1b2c3d4e5f6a7b8.iot.vendor.example", Count: 15}, // device ID
			{Domain: "123e4567-e89b-12d3-a456-426614174000.example", Count: 10},
			{Domain: "f7a3c9e1b2d4.iot.vendor.example", Count: 5}, // same after redaction
			{Domain: "api.vendor.example", Count: 1},
		},
	}
	raw := suggestURL(r)
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != newIssueURL {
		t.Errorf("link goes to %s, want %s", got, newIssueURL)
	}
	q := u.Query()
	if q.Get("template") != "new-device.yml" {
		t.Errorf("template = %q", q.Get("template"))
	}
	if q.Get("device") != "Apple, Inc. computer" || q.Get("title") != "Unclassified domains: Apple, Inc. computer" {
		t.Errorf("device = %q, title = %q", q.Get("device"), q.Get("title"))
	}
	want := "cdn.vendor.example\n*.dnsbl.example\n*.iot.vendor.example\napi.vendor.example"
	if got := q.Get("domains"); got != want {
		t.Errorf("domains =\n%s\nwant\n%s", got, want)
	}
	for k := range q {
		switch k {
		case "template", "title", "device", "domains":
		default:
			t.Errorf("unexpected field %q", k)
		}
	}

	// Nothing private, in any spelling, anywhere in the link.
	lower := strings.ToLower(raw)
	for _, secret := range []string{
		"192.168", "192-168", "168.192", "fd00", "3c:22", "3c22", "3c-22", "7e:90", "macbook", "dans", "dan%27s", "laptop",
		"a1b2c3d4", "123e4567", "f7a3c9e1",
	} {
		for _, form := range []string{secret, url.QueryEscape(secret)} {
			if strings.Contains(lower, strings.ToLower(form)) {
				t.Errorf("issue link contains %q: %s", form, raw)
			}
		}
	}
}

func TestSuggestURLEmpty(t *testing.T) {
	r := model.DeviceReport{Unknown: []model.UnknownDomain{{Domain: "12345.67890.example"}}}
	if got := suggestURL(r); got != "" {
		t.Errorf("suggestURL = %q, want empty: nothing identifiable is left to share", got)
	}
	if got := suggestURL(model.DeviceReport{}); got != "" {
		t.Errorf("suggestURL with no unknowns = %q", got)
	}
}

func TestSuggestURLCapped(t *testing.T) {
	r := model.DeviceReport{Device: model.Device{Kind: model.KindUnknown}}
	for _, c := range "abcdefghijklmnopqrstuvwxyz" {
		r.Unknown = append(r.Unknown, model.UnknownDomain{Domain: string(c) + "x.example"})
	}
	u, _ := url.Parse(suggestURL(r))
	q := u.Query()
	if n := len(strings.Split(q.Get("domains"), "\n")); n != maxSuggest {
		t.Errorf("%d domains, want %d", n, maxSuggest)
	}
	if q.Get("device") != "" || q.Get("title") != "Unclassified domains" {
		t.Errorf("device = %q, title = %q for an unknown device", q.Get("device"), q.Get("title"))
	}
}
