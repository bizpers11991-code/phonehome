// Package config loads and validates phonehome's YAML configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Source types understood by phonehome.
const (
	TypePiholeDB        = "pihole-db"
	TypePiholeAPI       = "pihole-api"
	TypeAdGuardQueryLog = "adguard-querylog"
	TypeDnsmasqLog      = "dnsmasq-log"
	TypeLeases          = "leases"
	TypeConntrack       = "conntrack"
)

// MaxRetentionDays bounds retention_days (about 100 years), so that the
// retention period cannot overflow a time.Duration and wrap around to a
// short one that would prune almost everything.
const MaxRetentionDays = 36500

// DefaultConntrackPath is used when a conntrack source has no path.
const DefaultConntrackPath = "/proc/net/nf_conntrack"

var fileTypes = []string{TypePiholeDB, TypeAdGuardQueryLog, TypeDnsmasqLog, TypeLeases, TypeConntrack}

// Config is the whole configuration file.
type Config struct {
	Listen        string            `yaml:"listen"`
	DB            string            `yaml:"db"`
	RetentionDays int               `yaml:"retention_days"` // 0 keeps data forever
	Interval      time.Duration     `yaml:"interval"`
	QuietHours    QuietHours        `yaml:"quiet_hours"`
	Timezone      string            `yaml:"timezone"` // IANA name; "" = system local
	Auth          Auth              `yaml:"auth"`
	Sources       []Source          `yaml:"sources"`   // empty = auto-detect
	Labels        map[string]string `yaml:"labels"`    // MAC or IP → friendly name
	Resolvers     []string          `yaml:"resolvers"` // your own DNS servers' LAN IPs; exempt from DNS-bypass findings
	Metrics       bool              `yaml:"metrics"`   // serve /metrics for Prometheus (off by default)
}

// QuietHours is the local-time window [Start, End) in which a home is
// normally asleep. It may wrap midnight (e.g. 23 → 6).
type QuietHours struct {
	Start int `yaml:"start"`
	End   int `yaml:"end"`
}

// Auth protects the web UI with HTTP basic auth when Username is set.
type Auth struct {
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
	PasswordFile string `yaml:"password_file"`
}

// Source is one place phonehome reads observations from.
type Source struct {
	Type         string `yaml:"type"`
	Name         string `yaml:"name"` // defaults to Type; also the cursor key, so keep it stable
	Path         string `yaml:"path"`
	URL          string `yaml:"url"`
	Password     string `yaml:"password"`
	PasswordFile string `yaml:"password_file"`
}

// Default returns the configuration used when no file is given.
func Default() *Config {
	return &Config{
		Listen:        ":8099",
		DB:            "/var/lib/phonehome/phonehome.db",
		RetentionDays: 90,
		Interval:      60 * time.Second,
		QuietHours:    QuietHours{Start: 1, End: 6},
	}
}

// Load reads the file at path on top of Default and validates the result.
// A missing file yields an error wrapping fs.ErrNotExist.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	c, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return c, nil
}

// Parse decodes YAML on top of Default, fills per-source defaults and
// validates. Unknown keys are rejected so typos do not silently do nothing.
func Parse(b []byte) (*Config, error) {
	c := Default()
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	for i := range c.Sources {
		c.Sources[i].fillDefaults()
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Source) fillDefaults() {
	s.Type = strings.TrimSpace(strings.ToLower(s.Type))
	if s.Name == "" {
		s.Name = s.Type
	}
	if s.Type == TypeConntrack && s.Path == "" {
		s.Path = DefaultConntrackPath
	}
}

// ApplyEnv overrides settings from PHONEHOME_LISTEN and PHONEHOME_DB.
func (c *Config) ApplyEnv(getenv func(string) string) {
	if v := getenv("PHONEHOME_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := getenv("PHONEHOME_DB"); v != "" {
		c.DB = v
	}
}

// Validate reports every problem in c, each with a hint on how to fix it.
func (c *Config) Validate() error {
	var errs []error
	bad := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	if c.Listen == "" {
		bad(`listen is empty; set it to an address like ":8099" or "127.0.0.1:8099"`)
	} else if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		bad(`listen %q is not host:port; use something like ":8099"`, c.Listen)
	}
	if c.DB == "" {
		bad("db is empty; set it to a writable file path such as /var/lib/phonehome/phonehome.db")
	}
	if c.RetentionDays < 0 || c.RetentionDays > MaxRetentionDays {
		bad("retention_days is %d; use a number of days up to %d, or 0 to keep everything", c.RetentionDays, MaxRetentionDays)
	}
	if c.Interval < time.Second {
		bad(`interval %v is too short; use a duration of at least 1s, e.g. "60s"`, c.Interval)
	}
	q := c.QuietHours
	if q.Start < 0 || q.Start > 23 || q.End < 0 || q.End > 23 {
		bad("quiet_hours {start: %d, end: %d} must use hours 0–23", q.Start, q.End)
	} else if q.Start == q.End {
		bad("quiet_hours start and end are both %d; the window would be empty", q.Start)
	}
	if _, err := c.Location(); err != nil {
		bad(`timezone %q is not a known IANA zone; use a name like "Europe/Berlin" or leave it empty for the system zone`, c.Timezone)
	}
	if err := c.Auth.validate(); err != nil {
		errs = append(errs, err)
	}

	names := map[string]int{}
	for i, s := range c.Sources {
		if err := s.validate(); err != nil {
			errs = append(errs, fmt.Errorf("sources[%d]: %w", i, err))
		}
		if j, dup := names[s.Name]; dup && s.Name != "" {
			bad(`sources[%d] and sources[%d] are both named %q; give each a distinct "name:"`, j, i, s.Name)
		}
		names[s.Name] = i
	}

	for _, r := range c.Resolvers {
		if _, err := netip.ParseAddr(strings.TrimSpace(r)); err != nil {
			bad(`resolvers: %q is not an IP address; list the LAN address of your Pi-hole or AdGuard box`, r)
		}
	}
	for k := range c.Labels {
		if _, ok := deviceID(k); !ok {
			bad(`labels: %q is neither a MAC address (aa:bb:cc:dd:ee:ff) nor an IP address`, k)
		}
	}
	return errors.Join(errs...)
}

func (a Auth) validate() error {
	switch {
	case a.Password != "" && a.PasswordFile != "":
		return errors.New("auth: set either password or password_file, not both")
	case a.Username == "" && (a.Password != "" || a.PasswordFile != ""):
		return errors.New("auth: a password is set but username is empty; set auth.username")
	case a.Username != "" && a.Password == "" && a.PasswordFile == "":
		return errors.New("auth: username is set but there is no password; set auth.password_file (preferred) or auth.password")
	}
	return nil
}

func (s Source) validate() error {
	if s.Name == "" {
		return errors.New(`name is empty; set "name:" or "type:"`)
	}
	if s.Password != "" && s.PasswordFile != "" {
		return fmt.Errorf("%s: set either password or password_file, not both", s.Name)
	}
	switch s.Type {
	case "":
		return fmt.Errorf("%s: type is missing; use one of %s", s.Name, typeList())
	case TypePiholeAPI:
		if s.URL == "" {
			return fmt.Errorf(`%s: pihole-api needs "url:", e.g. http://pi.hole`, s.Name)
		}
		u, err := url.Parse(s.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%s: url %q must look like http://pi.hole or https://192.168.1.2", s.Name, s.URL)
		}
		if s.Path != "" {
			return fmt.Errorf(`%s: pihole-api reads over HTTP and takes no "path:"; use type pihole-db to read the database file`, s.Name)
		}
	default:
		if !isFileType(s.Type) {
			return fmt.Errorf("%s: unknown type %q; use one of %s", s.Name, s.Type, typeList())
		}
		if s.Path == "" {
			return fmt.Errorf(`%s: %s needs "path:" pointing at the file to read`, s.Name, s.Type)
		}
		if s.URL != "" || s.Password != "" || s.PasswordFile != "" {
			return fmt.Errorf("%s: %s reads a local file; remove url/password settings", s.Name, s.Type)
		}
	}
	return nil
}

func isFileType(t string) bool {
	for _, f := range fileTypes {
		if t == f {
			return true
		}
	}
	return false
}

func typeList() string {
	return strings.Join(append([]string{TypePiholeAPI}, fileTypes...), ", ")
}

// Secret returns the password, reading PasswordFile if set.
func (s Source) Secret() (string, error) { return secret(s.Password, s.PasswordFile) }

// Secret returns the password, reading PasswordFile if set.
func (a Auth) Secret() (string, error) { return secret(a.Password, a.PasswordFile) }

func secret(inline, file string) (string, error) {
	if file == "" {
		return inline, nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("reading password file: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// Location returns the configured time zone (time.Local when unset).
func (c *Config) Location() (*time.Location, error) {
	if c.Timezone == "" {
		return time.Local, nil
	}
	return time.LoadLocation(c.Timezone)
}

// Retention is RetentionDays as a duration (0 = keep forever).
func (c *Config) Retention() time.Duration {
	return time.Duration(c.RetentionDays) * 24 * time.Hour
}

// DeviceLabels maps model.Device IDs ("mac:…" or "ip:…") to configured labels.
func (c *Config) DeviceLabels() map[string]string {
	m := make(map[string]string, len(c.Labels))
	for k, v := range c.Labels {
		if id, ok := deviceID(k); ok {
			m[id] = v
		}
	}
	return m
}

// ResolverAddrs parses Resolvers, skipping invalid entries (Validate reports them).
func (c *Config) ResolverAddrs() []netip.Addr {
	var out []netip.Addr
	for _, r := range c.Resolvers {
		if a, err := netip.ParseAddr(strings.TrimSpace(r)); err == nil {
			out = append(out, a.Unmap())
		}
	}
	return out
}

func deviceID(key string) (string, bool) {
	key = strings.TrimSpace(key)
	if mac, err := net.ParseMAC(key); err == nil && len(mac) == 6 {
		return "mac:" + mac.String(), true
	}
	if ip, err := netip.ParseAddr(key); err == nil {
		return "ip:" + ip.Unmap().String(), true
	}
	return "", false
}
