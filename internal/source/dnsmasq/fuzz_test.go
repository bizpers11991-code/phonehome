package dnsmasq

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedLines adds every line of the testdata logs to f's corpus.
func seedLines(f *testing.F) {
	for _, name := range []string{"syslog.log", "extra.log", "mixed.log"} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatal(err)
		}
		for line := range bytes.Lines(b) {
			f.Add(string(line))
		}
	}
}

func FuzzParseLine(f *testing.F) {
	seedLines(f)
	f.Add("Oct  2 13:58:01 dnsmasq[1]: 7 192.168.1.20/5353 query[type=65] x.example from fe80::1%eth0")
	f.Add("2026-10-02T13:58:01Z h dnsmasq: reply 1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.d.f.ip6.arpa is x")
	f.Fuzz(func(t *testing.T, line string) {
		e, ok := parseLine(line, time.UTC, testNow)
		if !ok {
			return
		}
		if e.name == "" || e.key == "" {
			t.Fatalf("accepted %q with empty name or key: %+v", line, e)
		}
		if e.query && (!e.client.IsValid() || e.client.Zone() != "" || e.client.Is4In6()) {
			t.Fatalf("query %q has client %v", line, e.client)
		}
	})
}

// FuzzFetchDNS reads arbitrary log contents in small batches and checks
// that the cursor always settles, no batch exceeds its limit, and reading in
// batches gives the same lookups as reading everything at once.
func FuzzFetchDNS(f *testing.F) {
	for _, name := range []string{"syslog.log", "extra.log", "mixed.log"} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b, uint8(2))
	}
	f.Fuzz(func(t *testing.T, data []byte, limit uint8) {
		path := filepath.Join(t.TempDir(), "dnsmasq.log")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		l := newTestLog(path)
		l.name = "dnsmasq"
		all, _ := drain(t, l, "", 1<<20)
		some, _ := drain(t, l, "", int(limit%8)+1)
		if len(all) != len(some) {
			t.Fatalf("read %d lookups at once but %d in batches", len(all), len(some))
		}
	})
}
