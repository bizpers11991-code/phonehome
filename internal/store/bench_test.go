package store

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

const benchBatch = 1000

// synthDNS returns n lookups spread evenly over span starting at t0, from 30
// clients to a skewed set of 5000 domains, roughly like a busy home.
func synthDNS(n int, span time.Duration) []model.DNSQuery {
	r := rand.New(rand.NewPCG(1, 2))
	zipf := rand.NewZipf(r, 1.2, 1, 4999)
	step := span / time.Duration(n)
	qs := make([]model.DNSQuery, n)
	for i := range qs {
		qs[i] = model.DNSQuery{
			Time:     t0.Add(time.Duration(i) * step),
			ClientIP: netip.AddrFrom4([4]byte{192, 168, 1, byte(10 + r.IntN(30))}),
			Domain:   fmt.Sprintf("host%d.vendor%d.example.com", zipf.Uint64(), r.IntN(3)),
			QType:    []string{"A", "AAAA", "HTTPS"}[r.IntN(3)],
			Blocked:  r.IntN(10) == 0,
			Source:   "pihole",
		}
	}
	return qs
}

func insertAll(b *testing.B, s *Store, qs []model.DNSQuery) {
	for i := 0; i < len(qs); i += benchBatch {
		if err := s.InsertDNS(context.Background(), qs[i:min(i+benchBatch, len(qs))]); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkInsertDNS measures batched inserts into a file database.
func BenchmarkInsertDNS(b *testing.B) {
	s, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	qs := synthDNS(benchBatch*100, 7*24*time.Hour)
	i := 0
	for b.Loop() {
		insertAll(b, s, qs[i:i+benchBatch])
		i = (i + benchBatch) % len(qs)
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*benchBatch), "ns/row")
}

// BenchmarkDNSBetween7Days stores 1M lookups over 30 days, then reads back a
// 7-day window (~233k rows). The one-off insert time and file size are logged.
func BenchmarkDNSBetween7Days(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bench.db")
	s, err := Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	const n = 1_000_000
	start := time.Now()
	insertAll(b, s, synthDNS(n, 30*24*time.Hour))
	elapsed := time.Since(start)
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		b.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("inserted %d rows in %v (%.0f rows/s); db file %.1f MiB (%.1f B/row)",
		n, elapsed.Round(time.Millisecond), n/elapsed.Seconds(), float64(fi.Size())/(1<<20), float64(fi.Size())/n)

	p := model.Period{From: t0.Add(10 * 24 * time.Hour), To: t0.Add(17 * 24 * time.Hour)}
	var rows int
	for b.Loop() {
		qs, err := s.DNSBetween(context.Background(), p)
		if err != nil {
			b.Fatal(err)
		}
		rows = len(qs)
	}
	b.ReportMetric(float64(rows), "rows/op")
}
