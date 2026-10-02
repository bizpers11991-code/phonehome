// Package oui names the manufacturer behind a MAC address.
//
// The table is a curated slice of the IEEE MA-L, MA-M and MA-S registries:
// only brands people own at home (TVs, speakers, cameras, plugs, phones,
// routers, ...) and the Wi-Fi chip makers whose name shows up for many cheap
// devices. Registry names are shortened to what people recognise, so
// "Hon Hai Precision Ind. Co.,Ltd." becomes "Foxconn".
//
// To refresh the table, run `go generate ./internal/oui` (it downloads the
// registries) and review the diff of vendors.txt.
package oui

//go:generate go run ./internal/gen

import (
	_ "embed"
	"strings"
	"sync"
)

//go:embed vendors.txt
var vendorsTxt string

// prefixLens are the registry block sizes in hex digits, longest first:
// MA-S (36 bits), MA-M (28 bits), MA-L (24 bits).
var prefixLens = [...]int{9, 7, 6}

var table = sync.OnceValue(func() map[string]string {
	m := make(map[string]string, strings.Count(vendorsTxt, "\n"))
	for line := range strings.Lines(vendorsTxt) {
		if strings.HasPrefix(line, "#") {
			continue
		}
		prefix, name, ok := strings.Cut(strings.TrimSuffix(line, "\n"), "\t")
		if ok {
			m[prefix] = name
		}
	}
	return m
})

// Lookup returns a short vendor name for mac ("Samsung", "Roku", "Espressif",
// ...) or "" when the MAC is malformed, randomized, or not a brand in the
// table. The most specific registry block wins.
func Lookup(mac string) string {
	n, ok := Normalize(mac)
	if !ok || localBit(n) {
		return ""
	}
	return lookup(table(), strings.ReplaceAll(n, ":", ""))
}

// lookup finds the longest registry prefix of a 12-digit lowercase hex MAC in t.
func lookup(t map[string]string, hex string) string {
	for _, l := range prefixLens {
		if name, ok := t[hex[:l]]; ok {
			return name
		}
	}
	return ""
}

// Normalize returns mac as lowercase colon-separated octets
// ("aa:bb:cc:dd:ee:ff"). It accepts colons, dashes, Cisco-style dots
// ("aabb.ccdd.eeff"), bare hex, any letter case, and single-digit octets
// ("a:b:c:d:e:f", as some tools print them).
func Normalize(mac string) (string, bool) {
	mac = strings.TrimSpace(mac)
	var groups []string
	switch {
	case strings.Contains(mac, ":"):
		groups = strings.Split(mac, ":")
	case strings.Contains(mac, "-"):
		groups = strings.Split(mac, "-")
	case strings.Contains(mac, "."):
		groups = strings.Split(mac, ".")
		if len(groups) != 3 {
			return "", false
		}
		for _, g := range groups {
			if len(g) != 4 {
				return "", false
			}
		}
		groups = []string{strings.Join(groups, "")}
	default:
		groups = []string{mac}
	}
	var hex []byte
	switch {
	case len(groups) == 1 && len(groups[0]) == 12:
		hex = []byte(groups[0])
	case len(groups) == 6:
		for _, g := range groups {
			switch len(g) {
			case 1:
				hex = append(hex, '0', g[0])
			case 2:
				hex = append(hex, g...)
			default:
				return "", false
			}
		}
	default:
		return "", false
	}
	out := make([]byte, 0, 17)
	for i, c := range hex {
		switch {
		case '0' <= c && c <= '9', 'a' <= c && c <= 'f':
		case 'A' <= c && c <= 'F':
			c += 'a' - 'A'
		default:
			return "", false
		}
		if i > 0 && i%2 == 0 {
			out = append(out, ':')
		}
		out = append(out, c)
	}
	return string(out), true
}

// Randomized reports whether mac is locally administered rather than
// assigned by a manufacturer. Phones, tablets and laptops use such "private"
// Wi-Fi addresses by default, so no vendor can be derived from them.
// It returns false for malformed input.
func Randomized(mac string) bool {
	n, ok := Normalize(mac)
	return ok && localBit(n)
}

// localBit reports whether the locally-administered bit (0x02 of the first
// octet, i.e. of the second hex digit) is set in a normalized MAC.
func localBit(n string) bool {
	return strings.IndexByte("2367abef", n[1]) >= 0
}
