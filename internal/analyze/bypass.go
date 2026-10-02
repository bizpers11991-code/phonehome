package analyze

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// Bypass kinds reported in model.Bypass.Kind.
const (
	BypassDoHLookup  = "doh-lookup"  // looked up an encrypted-DNS resolver
	BypassDoHFlow    = "doh-flow"    // connected to a public resolver on 443
	BypassDoTFlow    = "dot-flow"    // connected to port 853 (DNS over TLS)
	BypassForeignDNS = "foreign-dns" // plain DNS sent to a public server
)

// resolverHosts are hostnames of public encrypted-DNS (DoH/DoT) services. A
// lookup matches an entry or any subdomain of it, so "cloudflare-dns.com"
// also covers mozilla.cloudflare-dns.com and chrome.cloudflare-dns.com, and
// "dns.nextdns.io" covers per-user <id>.dns.nextdns.io.
//
// Deliberately absent: use-application-dns.net. It is Firefox's canary
// domain: Firefox asks the local resolver about it to learn whether it should
// *not* turn on DoH, so seeing it says nothing about a bypass.
var resolverHosts = []string{
	"dns.google",                   // Google Public DNS (also dns64.dns.google)
	"cloudflare-dns.com",           // Cloudflare
	"one.one.one.one",              // Cloudflare
	"dns.quad9.net",                // Quad9
	"dns9.quad9.net",               // Quad9 (secured)
	"dns10.quad9.net",              // Quad9 (unsecured)
	"dns11.quad9.net",              // Quad9 (with ECS)
	"doh.opendns.com",              // Cisco OpenDNS
	"doh.familyshield.opendns.com", // Cisco OpenDNS FamilyShield
	"dns.nextdns.io",               // NextDNS
	"dns.adguard-dns.com",          // AdGuard DNS
	"family.adguard-dns.com",       // AdGuard DNS (family)
	"unfiltered.adguard-dns.com",   // AdGuard DNS (unfiltered)
	"dns.adguard.com",              // AdGuard DNS (legacy name)
	"doh.cleanbrowsing.org",        // CleanBrowsing
	"dns.mullvad.net",              // Mullvad
	"doh.mullvad.net",              // Mullvad (legacy name)
	"dns.controld.com",             // Control D
	"freedns.controld.com",         // Control D
	"doh.dns.sb",                   // DNS.SB
	"dns.alidns.com",               // Alibaba
	"doh.pub",                      // Tencent DNSPod
}

func isResolverHost(domain string) bool {
	for _, h := range resolverHosts {
		if domain == h || strings.HasSuffix(domain, "."+h) {
			return true
		}
	}
	return false
}

// resolverIPs are anycast addresses of well-known public DNS resolvers. HTTPS
// to one of them is almost certainly DNS over HTTPS.
var resolverIPs = func() map[netip.Addr]bool {
	m := make(map[netip.Addr]bool)
	for _, s := range []string{
		"8.8.8.8", "8.8.4.4", "2001:4860:4860::8888", "2001:4860:4860::8844", // Google
		"1.1.1.1", "1.0.0.1", "2606:4700:4700::1111", "2606:4700:4700::1001", // Cloudflare
		"9.9.9.9", "149.112.112.112", "2620:fe::fe", "2620:fe::9", // Quad9
		"208.67.222.222", "208.67.220.220", "2620:119:35::35", "2620:119:53::53", // OpenDNS
		"94.140.14.14", "94.140.15.15", "2a10:50c0::ad1:ff", "2a10:50c0::ad2:ff", // AdGuard
	} {
		m[netip.MustParseAddr(s)] = true
	}
	return m
}()

// cgnat is 100.64.0.0/10 (RFC 6598): carrier-grade NAT and overlay networks
// such as Tailscale, not the public internet.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// isPublic reports whether a is a globally routable unicast address.
func isPublic(a netip.Addr) bool {
	return a.IsGlobalUnicast() && !a.IsPrivate() && !cgnat.Contains(a)
}

// flowBypass classifies one flow as DNS bypass evidence, if it is any.
func flowBypass(f *model.Flow) (kind, evidence string, ok bool) {
	ip := normAddr(f.RemoteIP)
	if !isPublic(ip) {
		return "", "", false
	}
	evidence = netip.AddrPortFrom(ip, f.RemotePort).String()
	switch {
	case f.RemotePort == 853:
		return BypassDoTFlow, evidence, true
	case f.RemotePort == 53:
		return BypassForeignDNS, evidence, true
	case f.RemotePort == 443 && resolverIPs[ip]:
		return BypassDoHFlow, evidence, true
	}
	return "", "", false
}

// bypassOrder lists bypass kinds strongest first; it is the report order.
var bypassOrder = []struct {
	kind, confidence, detail string
}{
	{BypassDoHFlow, "high", "Connected to the public DNS resolver %s over HTTPS, so its lookups can skip your DNS filter unseen."},
	{BypassDoTFlow, "high", "Connected to %s on the DNS-over-TLS port, so its lookups can skip your DNS filter unseen."},
	{BypassForeignDNS, "high", "Sent DNS lookups straight to %s instead of your own resolver."},
	{BypassDoHLookup, "medium", "Looked up %s, an encrypted-DNS service it can use to skip your DNS filter."},
}

// bypasses turns per-kind evidence counts into one Bypass per kind, citing
// the most frequent evidence (ties broken alphabetically).
func bypasses(seen map[string]map[string]int) []model.Bypass {
	var out []model.Bypass
	for _, b := range bypassOrder {
		ev := seen[b.kind]
		if len(ev) == 0 {
			continue
		}
		keys := make([]string, 0, len(ev))
		for k := range ev {
			keys = append(keys, k)
		}
		best := slices.MinFunc(keys, func(x, y string) int {
			return cmp.Or(cmp.Compare(ev[y], ev[x]), cmp.Compare(x, y))
		})
		out = append(out, model.Bypass{
			Kind:       b.kind,
			Detail:     fmt.Sprintf(b.detail, best),
			Evidence:   best,
			Confidence: b.confidence,
		})
	}
	return out
}
