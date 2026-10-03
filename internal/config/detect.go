package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
)

// Well-known locations probed by Detect, in priority order.
var (
	piholeDBPaths = []string{"/etc/pihole/pihole-FTL.db"}
	adguardPaths  = []string{
		"/opt/AdGuardHome/data/querylog.json",
		"/var/lib/AdGuardHome/data/querylog.json",
		"/var/snap/adguard-home/current/data/querylog.json",
		"/var/snap/adguard-home/common/data/querylog.json",
	}
	// dnsmasqLogPaths is only used when neither Pi-hole nor AdGuard Home is
	// found: Pi-hole's own dnsmasq writes the same lookups to its database,
	// and reading both would count every lookup twice.
	dnsmasqLogPaths = []string{"/var/log/dnsmasq.log"}
	leasesPaths     = []string{
		"/etc/pihole/dhcp.leases",
		"/var/lib/misc/dnsmasq.leases",
		"/tmp/dhcp.leases", // OpenWrt
	}
)

// Detection is the outcome of probing the well-known locations.
type Detection struct {
	Sources  []Source  // usable sources, at most one per type
	Problems []Problem // files that exist but cannot be used, for types with no usable file
}

// Problem is a well-known file that exists but phonehome cannot read.
type Problem struct {
	Type     string // source type the file would have been
	Path     string
	Err      string // what went wrong, e.g. "permission denied"
	Hint     string // how to fix it, in one or two sentences
	Optional bool   // the source only adds detail (device names, connections)
}

// String is the problem as one log or terminal line.
func (p Problem) String() string {
	return fmt.Sprintf("found %s but %s: %s", p.Path, p.Err, p.Hint)
}

// HasDNS reports whether the detection found a source of DNS lookups,
// which is what the dashboard needs to show anything.
func (d Detection) HasDNS() bool {
	for _, s := range d.Sources {
		if IsDNSType(s.Type) {
			return true
		}
	}
	return false
}

// IsDNSType reports whether sources of type t provide DNS lookups.
func IsDNSType(t string) bool {
	switch t {
	case TypePiholeDB, TypePiholeAPI, TypeAdGuardQueryLog, TypeDnsmasqLog:
		return true
	}
	return false
}

// Detect probes the well-known files and returns a source for the first
// usable path of each type, so the result never has duplicate names. For a
// type with no usable path, every path that exists but cannot be read is
// reported as a Problem with a hint. probe returns nil for a readable
// regular file and an error wrapping fs.ErrNotExist when nothing is there;
// pass Check for the real filesystem.
func Detect(probe func(path string) error) Detection {
	var d Detection
	first := func(typ string, paths []string, optional bool) {
		var problems []Problem
		for _, p := range paths {
			err := probe(p)
			if err == nil {
				d.Sources = append(d.Sources, Source{Type: typ, Name: typ, Path: p})
				return
			}
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			problems = append(problems, problemFor(typ, p, err, optional))
		}
		d.Problems = append(d.Problems, problems...)
	}
	first(TypePiholeDB, piholeDBPaths, false)
	first(TypeAdGuardQueryLog, adguardPaths, false)
	if !d.HasDNS() {
		first(TypeDnsmasqLog, dnsmasqLogPaths, false)
	}
	first(TypeLeases, leasesPaths, true)
	first(TypeConntrack, []string{DefaultConntrackPath}, true)
	return d
}

// UnreadableError describes a file that exists but cannot be opened for
// reading. Check fills in its owner and mode when it can stat the file.
type UnreadableError struct {
	Path  string
	Mode  fs.FileMode // 0 when unknown
	GID   int         // -1 when unknown
	Group string      // group name, "" when unknown
	Err   error
}

func (e *UnreadableError) Error() string { return e.Path + ": " + e.Err.Error() }
func (e *UnreadableError) Unwrap() error { return e.Err }

// errNotRegular is returned by Check for a directory or device. Docker
// creates an empty directory when a bind-mounted file does not exist yet.
var errNotRegular = errors.New("it is not a regular file")

// Check reports whether path is a regular file the process can open for
// reading. It opens and immediately closes the file; nothing is read. A
// missing file yields an error wrapping fs.ErrNotExist; a file that exists
// but cannot be opened yields an *UnreadableError.
func Check(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return err
		}
		ue := &UnreadableError{Path: path, GID: -1, Err: err}
		var pe *fs.PathError
		if errors.As(err, &pe) {
			ue.Err = pe.Err
		}
		if fi, serr := os.Stat(path); serr == nil {
			ue.Mode = fi.Mode()
			ue.GID, ue.Group = fileGroup(fi)
		}
		return ue
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return &UnreadableError{Path: path, GID: -1, Err: err}
	}
	if !fi.Mode().IsRegular() {
		return &UnreadableError{Path: path, Mode: fi.Mode(), GID: -1, Err: errNotRegular}
	}
	return nil
}

// Readable reports whether path is a regular file the process can open for
// reading. It opens and immediately closes the file; nothing is read.
func Readable(path string) bool { return Check(path) == nil }

// problemFor turns a probe error into a Problem with an actionable hint.
func problemFor(typ, path string, err error, optional bool) Problem {
	p := Problem{Type: typ, Path: path, Err: err.Error(), Optional: optional}
	ue := &UnreadableError{GID: -1}
	if errors.As(err, &ue) {
		p.Err = ue.Err.Error()
	}
	switch {
	case errors.Is(err, errNotRegular):
		p.Err = "it is a directory or device, not a file"
		p.Hint = "if this is a Docker bind mount, the file did not exist when the container started; " +
			"mount the folder that contains it instead, or create the file first and restart the container"
		return p
	case !errors.Is(err, fs.ErrPermission):
		p.Hint = "check that the file is intact and that phonehome may read it"
		return p
	}

	group := "the file's group"
	if ue.GID >= 0 {
		group = "the file's group (GID " + strconv.Itoa(ue.GID) + ")"
	}
	gid := "<GID>"
	if ue.GID >= 0 {
		gid = strconv.Itoa(ue.GID)
	}
	name := ue.Group
	if name == "" {
		name = "<group>"
	}
	groupReadable := ue.Mode == 0 || ue.Mode.Perm()&0o040 != 0
	runWithGroup := fmt.Sprintf(`run phonehome with %s, e.g. Docker group_add: ["%s"] or systemd SupplementaryGroups=%s`, group, gid, name)

	switch typ {
	case TypePiholeDB:
		if ue.Group == "" {
			name = "pihole"
		}
		if ue.GID < 0 {
			gid = "1000" // the official pihole/pihole image
		}
		p.Hint = fmt.Sprintf(`Pi-hole v6 lets only its group read the database; run phonehome with %s, `+
			`e.g. Docker group_add: ["%s"] or systemd SupplementaryGroups=%s`, group, gid, name)
	case TypeAdGuardQueryLog:
		if groupReadable && ue.Mode != 0 {
			p.Hint = runWithGroup
		} else {
			p.Hint = `AdGuard Home writes its query log readable by root only; run phonehome as root with every ` +
				`capability dropped (Docker: user: "0:0" and cap_drop: [ALL]), see docs/setup/adguard-home.md`
		}
	case TypeConntrack:
		p.Hint = "only root can read the connection table; it is optional and only used to spot devices " +
			"bypassing your DNS, see docs/detectors.md"
	default:
		if groupReadable {
			p.Hint = runWithGroup
		} else {
			p.Hint = "only its owner can read it; make it group-readable (chmod g+r) and run phonehome with that group"
		}
	}
	return p
}
