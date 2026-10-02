package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// FuzzParse checks that any input either fails to parse or yields a
// configuration that validates and whose derived values are usable.
func FuzzParse(f *testing.F) {
	b, err := os.ReadFile(filepath.Join("..", "..", "packaging", "phonehome.example.yaml"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(b)
	f.Add([]byte("sources:\n  - type: pihole-api\n    url: http://pi.hole\n    password_file: /x\nlabels:\n  \"aa:bb:cc:dd:ee:ff\": TV\n"))
	f.Add([]byte("retention_days: 7\ninterval: 5m\nquiet_hours: {start: 23, end: 6}\ntimezone: Europe/Berlin\n"))
	f.Add([]byte("listen: &a \":1\"\ndb: *a\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		c, err := Parse(data)
		if err != nil {
			return
		}
		if err := c.Validate(); err != nil {
			t.Fatalf("Parse accepted a config that does not validate: %v", err)
		}
		if _, err := c.Location(); err != nil {
			t.Fatal(err)
		}
		if r := c.Retention(); r < 0 || int(r/(24*time.Hour)) != c.RetentionDays {
			t.Fatalf("retention_days %d gives retention %v", c.RetentionDays, r)
		}
		_ = c.DeviceLabels()
		_ = c.ResolverAddrs()
	})
}
