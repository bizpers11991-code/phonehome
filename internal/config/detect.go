package config

import "os"

// Well-known locations probed by AutoDetect, in priority order.
var (
	piholeDBPaths = []string{"/etc/pihole/pihole-FTL.db"}
	adguardPaths  = []string{
		"/opt/AdGuardHome/data/querylog.json",
		"/var/lib/AdGuardHome/data/querylog.json",
		"/var/snap/adguard-home/current/data/querylog.json",
		"/var/snap/adguard-home/common/data/querylog.json",
	}
	leasesPaths = []string{
		"/etc/pihole/dhcp.leases",
		"/var/lib/misc/dnsmasq.leases",
		"/tmp/dhcp.leases", // OpenWrt
	}
)

// AutoDetect returns sources for the well-known files that readable reports
// as present and readable. It picks at most one source per type, so the
// result never has duplicate names. Pass Readable for the real filesystem.
func AutoDetect(readable func(path string) bool) []Source {
	var out []Source
	first := func(typ string, paths []string) {
		for _, p := range paths {
			if readable(p) {
				out = append(out, Source{Type: typ, Name: typ, Path: p})
				return
			}
		}
	}
	first(TypePiholeDB, piholeDBPaths)
	first(TypeAdGuardQueryLog, adguardPaths)
	first(TypeLeases, leasesPaths)
	first(TypeConntrack, []string{DefaultConntrackPath})
	return out
}

// Readable reports whether path is a regular file the process can open for
// reading. It opens and immediately closes the file; nothing is read.
func Readable(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	return err == nil && fi.Mode().IsRegular()
}
