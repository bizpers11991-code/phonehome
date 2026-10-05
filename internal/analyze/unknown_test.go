package analyze

import (
	"fmt"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

func TestUnknownDomains(t *testing.T) {
	qs := []model.DNSQuery{
		q(3*time.Hour, "10.0.0.5", "a.mystery.example"),
		q(1*time.Hour, "10.0.0.5", "a.mystery.example"),
		q(2*time.Hour, "10.0.0.5", "a.mystery.example"),
		q(time.Hour, "10.0.0.5", "b.mystery.example"),
		q(time.Hour, "10.0.0.5", "acr.samsung.example"),          // classified
		q(time.Hour, "10.0.0.5", "5.0.0.10.in-addr.arpa"),        // reverse lookup
		q(time.Hour, "10.0.0.5", "nas.lan"),                      // local name
		q(time.Hour, "10.0.0.5", "printer"),                      // single label
		q(time.Hour, "10.0.0.5", "_dns.resolver.arpa"),           // DDR probe
		q(time.Hour, "10.0.0.5", "router.home.arpa"),             // RFC 8375
		q(8*24*time.Hour, "10.0.0.5", "late.mystery.example"),    // outside the period
		q(time.Hour, "10.0.0.6", "other-device.mystery.example"), // another device
	}
	r := Analyze(newFakeKB(), week, nil, qs, nil, utcOpt)
	d := findDevice(t, r, "ip:10.0.0.5")
	want := []model.UnknownDomain{
		{Domain: "a.mystery.example", Group: "mystery.example", Count: 3, First: t0.Add(time.Hour), Last: t0.Add(3 * time.Hour)},
		{Domain: "b.mystery.example", Group: "mystery.example", Count: 1, First: t0.Add(time.Hour), Last: t0.Add(time.Hour)},
	}
	if len(d.Unknown) != len(want) {
		t.Fatalf("Unknown = %+v, want %+v", d.Unknown, want)
	}
	for i, w := range want {
		g := d.Unknown[i]
		if g.Domain != w.Domain || g.Group != w.Group || g.Count != w.Count || !g.First.Equal(w.First) || !g.Last.Equal(w.Last) {
			t.Errorf("Unknown[%d] = %+v, want %+v", i, g, w)
		}
	}
	// The unknown category still counts every unclassified lookup, local or not.
	if got := d.ByCategory[model.CatUnknown]; got != 9 {
		t.Errorf("ByCategory[unknown] = %d, want 9", got)
	}
}

func TestUnknownCapped(t *testing.T) {
	var qs []model.DNSQuery
	for i := range maxUnknown + 10 {
		for k := range i + 1 {
			qs = append(qs, q(time.Hour+time.Duration(k)*time.Minute, "10.0.0.5", fmt.Sprintf("d%03d.example", i)))
		}
	}
	d := findDevice(t, Analyze(newFakeKB(), week, nil, qs, nil, utcOpt), "ip:10.0.0.5")
	if len(d.Unknown) != maxUnknown {
		t.Fatalf("len(Unknown) = %d, want %d", len(d.Unknown), maxUnknown)
	}
	if d.Unknown[0].Domain != fmt.Sprintf("d%03d.example", maxUnknown+9) {
		t.Errorf("Unknown[0] = %s, want the most looked-up domain", d.Unknown[0].Domain)
	}
}

func TestRegistrable(t *testing.T) {
	for in, want := range map[string]string{
		"example.com":                        "example.com",
		"a.b.example.com":                    "example.com",
		"x.example.co.uk":                    "example.co.uk",
		"example.co.uk":                      "example.co.uk",
		"api.vendor.com.au":                  "vendor.com.au",
		"a.b.c.tv":                           "c.tv",
		"bucket.s3.amazonaws.com":            "s3.amazonaws.com",
		"abc123.iot.us-east-1.amazonaws.com": "us-east-1.amazonaws.com",
		"d111111abcdef8.cloudfront.net":      "d111111abcdef8.cloudfront.net",
		"cdn.d111111abcdef8.cloudfront.net":  "d111111abcdef8.cloudfront.net",
		"localhost":                          "localhost",
	} {
		if got := Registrable(in); got != want {
			t.Errorf("Registrable(%q) = %q, want %q", in, got, want)
		}
	}
}
