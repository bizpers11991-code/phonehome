package oui

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"aa:bb:cc:dd:ee:ff", "aa:bb:cc:dd:ee:ff", true},
		{"AA:BB:CC:DD:EE:FF", "aa:bb:cc:dd:ee:ff", true},
		{"Aa-bB-cc-DD-ee-0F", "aa:bb:cc:dd:ee:0f", true},
		{"aabb.ccdd.eeff", "aa:bb:cc:dd:ee:ff", true},
		{"AABB.CCDD.EEFF", "aa:bb:cc:dd:ee:ff", true},
		{"aabbccddeeff", "aa:bb:cc:dd:ee:ff", true},
		{"B827EB123456", "b8:27:eb:12:34:56", true},
		{"  b8:27:eb:12:34:56\n", "b8:27:eb:12:34:56", true},
		{"0:1b:c5:7:30:1", "00:1b:c5:07:30:01", true}, // leading zeros dropped, as some tools print
		{"", "", false},
		{"*", "", false},
		{"aa:bb:cc:dd:ee", "", false},
		{"aa:bb:cc:dd:ee:ff:00", "", false},
		{"aa:bb:cc:dd:ee:fg", "", false},
		{"aaa:bb:cc:dd:ee:f", "", false},
		{"aa:bb-cc:dd:ee:ff", "", false},
		{"aabb.ccdd.eef", "", false},
		{"aab.bccd.deeff", "", false},
		{"aabbccddeef", "", false},
		{"aabbccddeeffaa", "", false},
		{"01:23:45:67:89:ab:cd:ef:00:11", "", false}, // DHCPv6 DUID, not a MAC
	}
	for _, tt := range tests {
		got, ok := Normalize(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("Normalize(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestRandomized(t *testing.T) {
	tests := []struct {
		mac  string
		want bool
	}{
		{"b8:27:eb:00:00:01", false}, // Raspberry Pi
		{"02:00:00:00:00:01", true},
		{"da:a1:19:12:34:56", true}, // typical iOS/Android private address
		{"DA-A1-19-12-34-56", true},
		{"3e:22:fb:00:00:01", true},
		{"f6:00:00:00:00:00", true},
		{"01:00:5e:00:00:fb", false}, // multicast, but globally administered
		{"00:00:00:00:00:00", false},
		{"not a mac", false},
	}
	for _, tt := range tests {
		if got := Randomized(tt.mac); got != tt.want {
			t.Errorf("Randomized(%q) = %v, want %v", tt.mac, got, tt.want)
		}
	}
}

func TestLookup(t *testing.T) {
	tests := []struct {
		mac  string
		want string
	}{
		{"b8:27:eb:12:34:56", "Raspberry Pi"},
		{"DC-A6-32-00-00-01", "Raspberry Pi"},
		{"0017.8800.0001", "Philips Hue"},
		{"cc6ea4000001", "Samsung"},
		{"f0:27:2d:aa:bb:cc", "Amazon"},
		{"a4:c1:38:00:00:01", "Telink"},
		{"00:55:da:50:00:01", "Nanoleaf"}, // MA-M block 00-55-DA-5
		{"00:1b:c5:07:30:01", "tado"},     // MA-S block 00-1B-C5-07-3
		{"00:1b:c5:07:20:01", ""},         // neighbouring MA-S block, not a home brand
		{"00:00:5e:00:01:01", ""},         // IANA VRRP, not in the table
		{"da:a1:19:12:34:56", ""},         // randomized
		{"garbage", ""},
	}
	for _, tt := range tests {
		if got := Lookup(tt.mac); got != tt.want {
			t.Errorf("Lookup(%q) = %q, want %q", tt.mac, got, tt.want)
		}
	}
}

func TestLookupPrefersLongestPrefix(t *testing.T) {
	tab := map[string]string{
		"aabbcc":    "Large",
		"aabbcc1":   "Medium",
		"aabbcc123": "Small",
	}
	tests := map[string]string{
		"aabbcc123456": "Small",
		"aabbcc124456": "Medium",
		"aabbcc200000": "Large",
		"aabbcd000000": "",
	}
	for hex, want := range tests {
		if got := lookup(tab, hex); got != want {
			t.Errorf("lookup(%q) = %q, want %q", hex, got, want)
		}
	}
}

func TestTableWellFormed(t *testing.T) {
	tab := table()
	if len(tab) < 5000 {
		t.Fatalf("table has %d prefixes; vendors.txt looks truncated", len(tab))
	}
	for prefix, name := range tab {
		switch len(prefix) {
		case 6, 7, 9:
		default:
			t.Errorf("prefix %q: bad length", prefix)
		}
		if strings.Trim(prefix, "0123456789abcdef") != "" {
			t.Errorf("prefix %q: not lowercase hex", prefix)
		}
		if name == "" || strings.TrimSpace(name) != name {
			t.Errorf("prefix %q: bad name %q", prefix, name)
		}
	}
}
