// Package leases reads DHCP lease files to learn which device (MAC address,
// hostname, manufacturer) owns which IP address.
//
// Three formats are recognised automatically:
//
//   - dnsmasq, including Pi-hole's /etc/pihole/dhcp.leases and OpenWrt's
//     /tmp/dhcp.leases: one "<expiry> <mac> <ip> <hostname> <client-id>"
//     line per lease. DHCPv6 leases carry no MAC address and are skipped.
//   - ISC dhcpd's dhcpd.leases: "lease <ip> { ... }" blocks, appended as
//     leases change, so the last block for an address wins.
//   - odhcpd's state file (OpenWrt's /tmp/hosts/odhcpd): "# <iface> <mac>
//     ipv4 <hostname> ..." lines. Its DHCPv6 entries identify clients by DUID
//     only and are skipped.
//
// The file is re-read on every call and never written.
package leases

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/oui"
)

// File is a DHCP lease file. It implements source.DeviceSource.
type File struct {
	path string
}

// New returns a source reading the lease file at path.
func New(path string) *File {
	return &File{path: path}
}

// Name identifies the source, e.g. "leases:dhcp.leases".
func (f *File) Name() string {
	return "leases:" + filepath.Base(f.path)
}

// Devices returns one device per MAC address found in the lease file, sorted
// by ID. LastSeen is only set for ISC dhcpd leases, which record when the
// client last talked to the server; the other formats only store when a
// lease expires.
func (f *File) Devices(ctx context.Context) ([]model.Device, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		return nil, err
	}
	leases, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.path, err)
	}
	return devices(leases), nil
}

// lease is one IPv4 address assignment.
type lease struct {
	MAC      string // normalized, see oui.Normalize
	IP       netip.Addr
	Hostname string    // "" if the client sent none
	Seen     time.Time // last client contact; zero if the format does not record it
}

// errUnknownFormat is returned for non-empty files with no recognisable lease.
var errUnknownFormat = errors.New("not a dnsmasq, odhcpd or ISC dhcpd lease file")

// parse detects the format of a lease file and returns its leases in file
// order.
func parse(data []byte) ([]lease, error) {
	if isDhcpd(data) {
		return parseDhcpd(data)
	}
	return parseLines(data)
}

// isDhcpd reports whether data looks like an ISC dhcpd lease database.
func isDhcpd(data []byte) bool {
	for line := range bytes.Lines(data) {
		f := strings.Fields(string(line))
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		switch f[0] {
		case "lease", "lease6", "host", "server-duid", "authoring-byte-order", "failover":
			return true
		}
		return false
	}
	return false
}

// parseLines handles the line-oriented formats (dnsmasq and odhcpd), which
// can be told apart line by line.
func parseLines(data []byte) ([]lease, error) {
	var (
		out        []lease
		recognised bool // saw at least one line in a known format
		content    bool // saw at least one non-comment line
	)
	for line := range bytes.Lines(data) {
		f := strings.Fields(string(line))
		if len(f) == 0 {
			continue
		}
		if f[0] == "#" {
			// odhcpd: # <iface> <mac> ipv4 <hostname> <valid-until> <addr-hex> 32 <ip>/32 ...
			if len(f) >= 9 {
				recognised = true
				if l, ok := odhcpdLease(f[1:]); ok {
					out = append(out, l)
				}
			}
			continue
		}
		content = true
		switch {
		case f[0] == "duid" && len(f) == 2: // dnsmasq: the server's own DHCPv6 DUID
			recognised = true
		case len(f) == 5 && isDigits(f[0]):
			recognised = true
			if l, ok := dnsmasqLease(f); ok {
				out = append(out, l)
			}
		case len(f) >= 2:
			// odhcpd also writes hosts-file lines: <addr> <fqdn> <name>.
			if _, err := netip.ParseAddr(f[0]); err == nil {
				recognised = true
			}
		}
	}
	if content && !recognised {
		return nil, errUnknownFormat
	}
	return out, nil
}

// dnsmasqLease parses "<expiry> <mac> <ip> <hostname|*> <client-id|*>".
// DHCPv6 lines put an IAID where the MAC goes and fail the MAC check.
func dnsmasqLease(f []string) (lease, bool) {
	mac, ok := oui.Normalize(f[1])
	if !ok {
		return lease{}, false
	}
	ip, err := netip.ParseAddr(f[2])
	if err != nil || !ip.Is4() {
		return lease{}, false
	}
	return lease{MAC: mac, IP: ip, Hostname: hostname(f[3])}, true
}

// odhcpdLease parses the fields after "#" of an odhcpd DHCPv4 state line.
func odhcpdLease(f []string) (lease, bool) {
	if f[2] != "ipv4" {
		return lease{}, false // DHCPv6: f[1] is a DUID, not a MAC
	}
	mac, ok := oui.Normalize(f[1])
	if !ok {
		return lease{}, false
	}
	for _, a := range f[6:] {
		if p, err := netip.ParsePrefix(a); err == nil && p.Addr().Is4() {
			return lease{MAC: mac, IP: p.Addr(), Hostname: hostname(f[3])}, true
		}
	}
	return lease{}, false
}

// hostname cleans the placeholder values dnsmasq ("*") and odhcpd ("-") use
// for clients that sent no name.
func hostname(s string) string {
	if s == "*" || s == "-" {
		return ""
	}
	return s
}

func isDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// devices groups leases by MAC. The most recent lease (by Seen, then file
// order) supplies the first IP and the hostname.
func devices(leases []lease) []model.Device {
	// Newest first; the stable sort keeps later file entries ahead of earlier
	// ones when Seen is equal or unknown.
	slices.Reverse(leases)
	slices.SortStableFunc(leases, func(a, b lease) int { return b.Seen.Compare(a.Seen) })

	byMAC := map[string]*model.Device{}
	for _, l := range leases {
		d := byMAC[l.MAC]
		if d == nil {
			d = &model.Device{
				ID:       "mac:" + l.MAC,
				MAC:      l.MAC,
				Vendor:   oui.Lookup(l.MAC),
				LastSeen: l.Seen,
			}
			byMAC[l.MAC] = d
		}
		if !slices.Contains(d.IPs, l.IP) {
			d.IPs = append(d.IPs, l.IP)
		}
		if d.Hostname == "" {
			d.Hostname = l.Hostname
		}
	}
	out := make([]model.Device, 0, len(byMAC))
	for _, d := range byMAC {
		out = append(out, *d)
	}
	slices.SortFunc(out, func(a, b model.Device) int { return cmp.Compare(a.ID, b.ID) })
	return out
}
