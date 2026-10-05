package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefaultIsValid(t *testing.T) {
	c := Default()
	if err := c.Validate(); err != nil {
		t.Fatalf("Default().Validate() = %v", err)
	}
	if c.Listen != ":8099" || c.RetentionDays != 90 || c.Interval != time.Minute ||
		c.QuietHours != (QuietHours{1, 6}) || len(c.Sources) != 0 {
		t.Errorf("unexpected defaults: %+v", c)
	}
}

func TestParseEmptyKeepsDefaults(t *testing.T) {
	c, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, Default()) {
		t.Errorf("Parse(nil) = %+v, want defaults", c)
	}
}

func TestLoadFull(t *testing.T) {
	path := filepath.Join(t.TempDir(), "phonehome.yaml")
	writeFile(t, path, `
listen: "127.0.0.1:9000"
db: /tmp/x.db
retention_days: 30
interval: 2m
quiet_hours: {start: 23, end: 7}
timezone: Europe/Berlin
auth: {username: admin, password: hunter2}
sources:
  - type: pihole-db
    path: /etc/pihole/pihole-FTL.db
  - type: pihole-api
    name: upstairs
    url: http://pi.hole
    password_file: /etc/phonehome/pw
  - type: conntrack
labels:
  "AA-BB-CC-DD-EE-FF": Living room TV
  "192.168.1.50": Printer
`)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9000" || c.Interval != 2*time.Minute || c.RetentionDays != 30 {
		t.Errorf("scalars not loaded: %+v", c)
	}
	want := []Source{
		{Type: TypePiholeDB, Name: TypePiholeDB, Path: "/etc/pihole/pihole-FTL.db"},
		{Type: TypePiholeAPI, Name: "upstairs", URL: "http://pi.hole", PasswordFile: "/etc/phonehome/pw"},
		{Type: TypeConntrack, Name: TypeConntrack, Path: DefaultConntrackPath},
	}
	if !reflect.DeepEqual(c.Sources, want) {
		t.Errorf("sources = %+v\nwant %+v", c.Sources, want)
	}
	wantLabels := map[string]string{"mac:aa:bb:cc:dd:ee:ff": "Living room TV", "ip:192.168.1.50": "Printer"}
	if got := c.DeviceLabels(); !reflect.DeepEqual(got, wantLabels) {
		t.Errorf("DeviceLabels = %v, want %v", got, wantLabels)
	}
	loc, _ := c.Location()
	if loc.String() != "Europe/Berlin" {
		t.Errorf("Location = %v", loc)
	}
	if c.Retention() != 30*24*time.Hour {
		t.Errorf("Retention = %v", c.Retention())
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	_, err := Parse([]byte("listne: :8099\n"))
	if err == nil || !strings.Contains(err.Error(), "listne") {
		t.Fatalf("err = %v, want mention of the typo", err)
	}
}

func TestAllowedHosts(t *testing.T) {
	c, err := Parse([]byte(`allowed_hosts: [phonehome.example.com, "*.ts.net", nas.]`))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"phonehome.example.com", "*.ts.net", "nas."}; !reflect.DeepEqual(c.AllowedHosts, want) {
		t.Errorf("AllowedHosts = %q, want %q", c.AllowedHosts, want)
	}
}

func TestValidateMessages(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"bad listen", `listen: "8099"`, "not host:port"},
		{"empty db", `db: ""`, "db is empty"},
		{"negative retention", `retention_days: -1`, "retention_days"},
		{"huge retention", `retention_days: 106752`, "up to 36500"},
		{"short interval", `interval: 10ms`, "interval"},
		{"quiet range", `quiet_hours: {start: 1, end: 24}`, "0–23"},
		{"quiet empty", `quiet_hours: {start: 3, end: 3}`, "empty"},
		{"timezone", `timezone: Mars/Olympus`, "IANA"},
		{"auth no user", `auth: {password: x}`, "username is empty"},
		{"auth no pass", `auth: {username: x}`, "no password"},
		{"auth both", `auth: {username: x, password: y, password_file: z}`, "not both"},
		{"source no type", "sources:\n  - path: /x", "type"},
		{"source unknown type", "sources:\n  - type: bind9\n    path: /x", `unknown type "bind9"`},
		{"source no path", "sources:\n  - type: adguard-querylog", `needs "path:"`},
		{"api no url", "sources:\n  - type: pihole-api", `needs "url:"`},
		{"api bad url", "sources:\n  - type: pihole-api\n    url: pi.hole", "must look like"},
		{"api with path", "sources:\n  - type: pihole-api\n    url: http://pi.hole\n    path: /x", "pihole-db"},
		{"file with url", "sources:\n  - type: leases\n    path: /x\n    url: http://x", "remove url"},
		{"dup names", "sources:\n  - type: leases\n    path: /a\n  - type: leases\n    path: /b", "distinct"},
		{"bad label", "labels:\n  tv: TV", "neither a MAC"},
		{"allowed_hosts url", `allowed_hosts: ["https://ph.example.com"]`, "allowed_hosts"},
		{"allowed_hosts port", `allowed_hosts: ["ph.example.com:443"]`, "allowed_hosts"},
		{"allowed_hosts any", `allowed_hosts: ["*"]`, "allowed_hosts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestValidateReportsAllProblems(t *testing.T) {
	c := Default()
	c.Listen = ""
	c.DB = ""
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "listen") || !strings.Contains(err.Error(), "db") {
		t.Fatalf("err = %v, want both problems", err)
	}
}

func TestApplyEnv(t *testing.T) {
	c := Default()
	env := map[string]string{"PHONEHOME_LISTEN": ":1234", "PHONEHOME_DB": "/data/p.db"}
	c.ApplyEnv(func(k string) string { return env[k] })
	if c.Listen != ":1234" || c.DB != "/data/p.db" {
		t.Errorf("ApplyEnv: %+v", c)
	}
	c.ApplyEnv(func(string) string { return "" })
	if c.Listen != ":1234" {
		t.Error("empty env must not override")
	}
}

func TestSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pw")
	writeFile(t, path, "s3cret\n")
	got, err := Source{PasswordFile: path}.Secret()
	if err != nil || got != "s3cret" {
		t.Errorf("Secret = %q, %v", got, err)
	}
	if got, _ := (Auth{Password: "inline"}).Secret(); got != "inline" {
		t.Errorf("inline Secret = %q", got)
	}
	if _, err := (Auth{PasswordFile: path + ".missing"}).Secret(); err == nil {
		t.Error("missing password file: want error")
	}
}

func TestDetect(t *testing.T) {
	present := map[string]bool{
		"/etc/pihole/pihole-FTL.db":                         true,
		"/var/snap/adguard-home/current/data/querylog.json": true,
		"/var/log/dnsmasq.log":                              true, // Pi-hole found: not read twice
		"/var/lib/misc/dnsmasq.leases":                      true,
		"/tmp/dhcp.leases":                                  true, // lower priority, skipped
	}
	got := Detect(probeMap(present, nil))
	want := []Source{
		{Type: TypePiholeDB, Name: TypePiholeDB, Path: "/etc/pihole/pihole-FTL.db"},
		{Type: TypeAdGuardQueryLog, Name: TypeAdGuardQueryLog, Path: "/var/snap/adguard-home/current/data/querylog.json"},
		{Type: TypeLeases, Name: TypeLeases, Path: "/var/lib/misc/dnsmasq.leases"},
	}
	if !reflect.DeepEqual(got.Sources, want) || len(got.Problems) != 0 {
		t.Errorf("Detect = %+v\nwant %+v", got, want)
	}
	if !got.HasDNS() {
		t.Error("HasDNS = false with a Pi-hole database")
	}
	c := Default()
	c.Sources = got.Sources
	if err := c.Validate(); err != nil {
		t.Errorf("auto-detected sources do not validate: %v", err)
	}

	if got := Detect(probeMap(nil, nil)); len(got.Sources) != 0 || len(got.Problems) != 0 || got.HasDNS() {
		t.Errorf("nothing present: got %+v", got)
	}
	got = Detect(probeMap(map[string]bool{DefaultConntrackPath: true}, nil))
	if len(got.Sources) != 1 || got.Sources[0].Type != TypeConntrack || got.HasDNS() {
		t.Errorf("conntrack only: got %+v", got)
	}
	got = Detect(probeMap(map[string]bool{"/var/log/dnsmasq.log": true}, nil))
	if len(got.Sources) != 1 || got.Sources[0].Type != TypeDnsmasqLog || !got.HasDNS() {
		t.Errorf("dnsmasq log only: got %+v", got)
	}
}

func TestDetectProblems(t *testing.T) {
	denied := func(gid int, group string, mode fs.FileMode) error {
		return &UnreadableError{GID: gid, Group: group, Mode: mode, Err: fs.ErrPermission}
	}
	got := Detect(probeMap(
		map[string]bool{"/var/lib/AdGuardHome/data/querylog.json": true},
		map[string]error{
			"/etc/pihole/pihole-FTL.db":           denied(1000, "", 0o640),
			"/opt/AdGuardHome/data/querylog.json": denied(0, "root", 0o600), // another path is readable
			"/etc/pihole/dhcp.leases":             denied(999, "pihole", 0o640),
			DefaultConntrackPath:                  denied(0, "root", 0o440),
		}))
	if len(got.Sources) != 1 || got.Sources[0].Type != TypeAdGuardQueryLog {
		t.Fatalf("sources = %+v", got.Sources)
	}
	if len(got.Problems) != 3 {
		t.Fatalf("problems = %+v", got.Problems)
	}
	pi := got.Problems[0]
	if pi.Type != TypePiholeDB || pi.Err != "permission denied" || pi.Optional {
		t.Errorf("pihole problem = %+v", pi)
	}
	for _, want := range []string{"GID 1000", `group_add: ["1000"]`, "SupplementaryGroups=pihole"} {
		if !strings.Contains(pi.String(), want) {
			t.Errorf("pihole hint %q lacks %q", pi, want)
		}
	}
	if !strings.HasPrefix(pi.String(), "found /etc/pihole/pihole-FTL.db but permission denied: ") {
		t.Errorf("String = %q", pi)
	}
	if l := got.Problems[1]; l.Type != TypeLeases || !l.Optional || !strings.Contains(l.Hint, "SupplementaryGroups=pihole") {
		t.Errorf("leases problem = %+v", l)
	}
	if c := got.Problems[2]; c.Type != TypeConntrack || !c.Optional || !strings.Contains(c.Hint, "optional") {
		t.Errorf("conntrack problem = %+v", c)
	}

	// AdGuard's root-only log, no Pi-hole: the dnsmasq log is tried too.
	got = Detect(probeMap(nil, map[string]error{
		"/opt/AdGuardHome/data/querylog.json": denied(0, "root", 0o600),
		"/var/log/dnsmasq.log":                denied(4, "adm", 0o640),
	}))
	if len(got.Sources) != 0 || len(got.Problems) != 2 || got.HasDNS() {
		t.Fatalf("got %+v", got)
	}
	if h := got.Problems[0].Hint; !strings.Contains(h, `user: "0:0"`) || !strings.Contains(h, "cap_drop") {
		t.Errorf("adguard hint = %q", h)
	}
	if h := got.Problems[1].Hint; !strings.Contains(h, "GID 4") || !strings.Contains(h, "SupplementaryGroups=adm") {
		t.Errorf("dnsmasq hint = %q", h)
	}

	// Docker turns a bind-mounted file that did not exist into a directory.
	got = Detect(probeMap(nil, map[string]error{
		"/etc/pihole/pihole-FTL.db": &UnreadableError{GID: -1, Mode: fs.ModeDir, Err: errNotRegular},
	}))
	if len(got.Problems) != 1 || !strings.Contains(got.Problems[0].Err, "directory") || !strings.Contains(got.Problems[0].Hint, "bind mount") {
		t.Errorf("directory: %+v", got.Problems)
	}
}

// probeMap fakes the filesystem: readable paths, paths failing with an
// error, and everything else missing.
func probeMap(readable map[string]bool, errs map[string]error) func(string) error {
	return func(p string) error {
		if readable[p] {
			return nil
		}
		if err := errs[p]; err != nil {
			return err
		}
		return fs.ErrNotExist
	}
}

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	writeFile(t, f, "x")
	if err := Check(f); err != nil || !Readable(f) {
		t.Errorf("regular file: %v", err)
	}
	var ue *UnreadableError
	if err := Check(dir); !errors.As(err, &ue) || !errors.Is(err, errNotRegular) || Readable(dir) {
		t.Errorf("directory: %v", err)
	}
	if err := Check(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}

	if os.Geteuid() == 0 {
		t.Skip("root reads files regardless of their mode")
	}
	if err := os.Chmod(f, 0); err != nil {
		t.Fatal(err)
	}
	err := Check(f)
	if !errors.As(err, &ue) || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("mode 000: %v", err)
	}
	if ue.GID < 0 {
		t.Errorf("GID unknown for a file we own")
	}
	p := Detect(func(string) error { return err }).Problems[0]
	if p.Err != "permission denied" {
		t.Errorf("problem = %+v", p)
	}
}

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMetricsOption(t *testing.T) {
	c, err := Parse([]byte("metrics: true\n"))
	if err != nil || !c.Metrics {
		t.Fatalf("metrics: true gave %+v, %v", c, err)
	}
	if c, err := Parse(nil); err != nil || c.Metrics {
		t.Fatalf("metrics must be off by default: %+v, %v", c, err)
	}
}

func TestCheckSources(t *testing.T) {
	denied := &UnreadableError{GID: 999, Group: "pihole", Mode: 0o640, Err: fs.ErrPermission}
	got := CheckSources([]Source{
		{Type: TypePiholeDB, Path: "/etc/pihole/pihole-FTL.db"},
		{Type: TypeAdGuardQueryLog, Path: "/adguard/querylog.json"},
		{Type: TypeDnsmasqLog, Path: "/var/log/dnsmasq.log"}, // readable
		{Type: TypeLeases, Path: "/leases"},
		{Type: TypePiholeAPI, URL: "http://pi.hole"}, // nothing to check
	}, probeMap(map[string]bool{"/var/log/dnsmasq.log": true}, map[string]error{"/etc/pihole/pihole-FTL.db": denied}))
	if len(got) != 3 {
		t.Fatalf("problems = %+v", got)
	}
	if p := got[0]; p.Missing || !strings.Contains(p.String(), `group_add: ["999"]`) {
		t.Errorf("unreadable database: %+v", p)
	}
	if p := got[1]; !p.Missing || p.Optional || !strings.HasPrefix(p.String(), "/adguard/querylog.json does not exist: AdGuard Home creates it") {
		t.Errorf("missing query log: %q", p)
	}
	if p := got[2]; !p.Missing || !p.Optional || !strings.Contains(p.Hint, "check the path") {
		t.Errorf("missing leases: %+v", p)
	}
}
