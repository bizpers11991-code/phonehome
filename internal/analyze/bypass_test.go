package analyze

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

func TestIsResolverHost(t *testing.T) {
	tests := map[string]bool{
		"dns.google":                  true,
		"dns64.dns.google":            true,
		"mozilla.cloudflare-dns.com":  true,
		"chrome.cloudflare-dns.com":   true,
		"one.one.one.one":             true,
		"abc123.dns.nextdns.io":       true,
		"doh.cleanbrowsing.org":       true,
		"use-application-dns.net":     false, // Firefox canary: informational only
		"notcloudflare-dns.com":       false,
		"www.quad9.net":               false, // the website, not the resolver
		"google.com":                  false,
		"dns.google.evil.example.com": false,
		"":                            false,
	}
	for host, want := range tests {
		if got := isResolverHost(host); got != want {
			t.Errorf("isResolverHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestFlowBypass(t *testing.T) {
	tests := []struct {
		remote   string
		port     uint16
		kind     string
		evidence string
	}{
		{"8.8.8.8", 443, BypassDoHFlow, "8.8.8.8:443"},
		{"2606:4700:4700::1111", 443, BypassDoHFlow, "[2606:4700:4700::1111]:443"},
		{"::ffff:1.1.1.1", 443, BypassDoHFlow, "1.1.1.1:443"},
		{"8.8.8.8", 853, BypassDoTFlow, "8.8.8.8:853"},
		{"203.0.113.9", 853, BypassDoTFlow, "203.0.113.9:853"},
		{"8.8.8.8", 53, BypassForeignDNS, "8.8.8.8:53"},
		{"203.0.113.9", 53, BypassForeignDNS, "203.0.113.9:53"},
		{"203.0.113.9", 443, "", ""},    // ordinary HTTPS
		{"8.8.8.8", 80, "", ""},         // not DNS
		{"192.168.1.2", 53, "", ""},     // your own resolver
		{"192.168.1.2", 853, "", ""},    // DoT to a LAN resolver is not a bypass
		{"fd00::53", 53, "", ""},        // ULA
		{"100.100.100.100", 53, "", ""}, // CGNAT / Tailscale MagicDNS
		{"127.0.0.1", 53, "", ""},       // loopback
		{"fe80::1", 53, "", ""},         // link-local
		{"224.0.0.251", 5353, "", ""},   // mDNS
		{"255.255.255.255", 53, "", ""}, // broadcast
	}
	for _, tt := range tests {
		f := model.Flow{RemoteIP: netip.MustParseAddr(tt.remote), RemotePort: tt.port}
		kind, ev, ok := flowBypass(&f)
		if kind != tt.kind || ev != tt.evidence || ok != (tt.kind != "") {
			t.Errorf("%s:%d → (%q, %q, %v), want (%q, %q)", tt.remote, tt.port, kind, ev, ok, tt.kind, tt.evidence)
		}
	}
}

func TestBypassesInReport(t *testing.T) {
	flow := func(remote string, port uint16) model.Flow {
		return model.Flow{Start: t0.Add(time.Hour), ClientIP: ip("10.0.0.5"), RemoteIP: ip(remote), RemotePort: port, Proto: "udp"}
	}
	fl := []model.Flow{
		flow("8.8.4.4", 53), flow("8.8.8.8", 53), flow("8.8.8.8", 53), // most frequent wins
		flow("1.1.1.1", 443),
		flow("9.9.9.9", 853),
	}
	qs := []model.DNSQuery{
		q(0, "10.0.0.5", "dns.google"),
		q(0, "10.0.0.5", "use-application-dns.net"),
		q(0, "10.0.0.6", "use-application-dns.net"),
	}
	r := Analyze(newFakeKB(), week, nil, qs, fl, utcOpt)

	d := findDevice(t, r, "ip:10.0.0.5")
	var got []string
	for _, b := range d.Bypasses {
		got = append(got, b.Kind+" "+b.Evidence+" "+b.Confidence)
		if b.Detail == "" {
			t.Errorf("%s: empty Detail", b.Kind)
		}
	}
	want := []string{
		"doh-flow 1.1.1.1:443 high",
		"dot-flow 9.9.9.9:853 high",
		"foreign-dns 8.8.8.8:53 high",
		"doh-lookup dns.google medium",
	}
	if !slices.Equal(got, want) {
		t.Errorf("bypasses = %v, want %v", got, want)
	}
	if d.Flows != 5 || d.Grade != "D" {
		t.Errorf("Flows = %d, Grade = %s; want 5, D", d.Flows, d.Grade)
	}
	if b := findDevice(t, r, "ip:10.0.0.6").Bypasses; len(b) != 0 {
		t.Errorf("Firefox canary flagged: %+v", b)
	}
}

func TestResolversExemptFromFlowBypass(t *testing.T) {
	flow := func(client, remote string, port uint16) model.Flow {
		return model.Flow{Start: t0.Add(time.Hour), ClientIP: ip(client), RemoteIP: ip(remote), RemotePort: port, Proto: "udp"}
	}
	fl := []model.Flow{
		flow("::ffff:10.0.0.2", "9.9.9.9", 53), // the Pi-hole forwarding upstream
		flow("10.0.0.2", "1.1.1.1", 853),
		flow("10.0.0.2", "8.8.8.8", 443),
		flow("10.0.0.5", "8.8.8.8", 53), // a TV going around it
	}
	qs := []model.DNSQuery{q(0, "10.0.0.2", "dns.quad9.net")}
	o := utcOpt
	o.Resolvers = []netip.Addr{ip("10.0.0.2")}
	r := Analyze(newFakeKB(), week, nil, qs, fl, o)

	pihole := findDevice(t, r, "ip:10.0.0.2")
	if pihole.Flows != 3 {
		t.Errorf("resolver Flows = %d, want 3 (still counted)", pihole.Flows)
	}
	// Only the lookup-based finding remains, and it does not lower the grade.
	if len(pihole.Bypasses) != 1 || pihole.Bypasses[0].Kind != BypassDoHLookup || pihole.Grade != "A" {
		t.Errorf("resolver bypasses = %+v, grade %s; want only doh-lookup, grade A", pihole.Bypasses, pihole.Grade)
	}
	if tv := findDevice(t, r, "ip:10.0.0.5"); len(tv.Bypasses) != 1 || tv.Bypasses[0].Kind != BypassForeignDNS {
		t.Errorf("other device bypasses = %+v, want foreign-dns", tv.Bypasses)
	}
}
