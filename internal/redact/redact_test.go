package redact

import (
	"net/netip"
	"testing"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

func TestDomain(t *testing.T) {
	d := model.Device{
		MAC:      "3C:22:FB:7E:90:AA",
		IPs:      []netip.Addr{netip.MustParseAddr("192.168.1.105"), netip.MustParseAddr("fd00::1:2")},
		Hostname: "Annas-iPhone.lan",
		Label:    "Anna's phone",
	}
	for _, tt := range []struct{ in, want string }{
		{"log.vendor.example", "log.vendor.example"},
		{"LOG.Vendor.Example", "log.vendor.example"},
		{"a1b2c3d4e5f6.iot.vendor.example", "*.iot.vendor.example"},
		{"105.1.168.192.dnsbl.example", "*.dnsbl.example"},
		{"123e4567-e89b-12d3-a456-426614174000.example", ""}, // one name left
		{"annas-iphone.lan", ""},                             // local name
		{"router.home.arpa", ""},                             // local zone
		{"105.1.168.192.in-addr.arpa", ""},                   // reverse lookup
		{"_dns.resolver.arpa", ""},                           // DDR probe
		{"annas-iphone.corp.example", ""},                    // hostname + search domain
		{"3c22fb7e90aa.tracker.example", ""},                 // MAC, bare
		{"id-3c-22-fb-7e-90-aa.tracker.example", ""},         // MAC, dashed
		{"tv.3c22.fb7e.90aa.tracker.example", ""},            // MAC, Cisco style
		{"host-192-168-1-105.isp.example", ""},               // address, dashed
		{"fd00--1-2.isp.example", ""},                        // IPv6 address, dashed
		{"dan's-laptop.example", ""},                         // not a valid name
		{"localhost", ""},
	} {
		if got := Domain(tt.in, d); got != tt.want {
			t.Errorf("Domain(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIDLike(t *testing.T) {
	for lb, want := range map[string]bool{
		"api": false, "cdn": false, "acr0": false, "us-east-1": false, "log-ingestion": false,
		"samsungcloudsolution": false, "fe2": false, "decade": false,
		"105": true, "a1b2c3d4": true, "3c22fb7e90aa": true, "123e4567-e89b-12d3": true,
		"dev7x9k2m4q8z1p0w3": true,
	} {
		if got := idLike(lb); got != want {
			t.Errorf("idLike(%q) = %v, want %v", lb, got, want)
		}
	}
}
