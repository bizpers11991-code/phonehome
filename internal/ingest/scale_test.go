package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/analyze"
	"github.com/bizpers11991-code/phonehome/internal/demo"
	"github.com/bizpers11991-code/phonehome/internal/kb"
	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/source"
	"github.com/bizpers11991-code/phonehome/internal/source/pihole"
	"github.com/bizpers11991-code/phonehome/internal/store"
)

// ftlSchema is the part of Pi-hole v6's pihole-FTL.db that phonehome reads,
// with FTL's own indexes.
const ftlSchema = `
CREATE TABLE query_storage (id INTEGER PRIMARY KEY AUTOINCREMENT, timestamp INTEGER NOT NULL, type INTEGER NOT NULL,
	status INTEGER NOT NULL, domain INTEGER NOT NULL, client INTEGER NOT NULL, forward INTEGER, additional_info INTEGER,
	reply_type INTEGER, reply_time REAL, dnssec INTEGER, list_id INTEGER, ede INTEGER);
CREATE INDEX idx_queries_timestamps ON query_storage (timestamp);
CREATE TABLE domain_by_id (id INTEGER PRIMARY KEY, domain TEXT NOT NULL);
CREATE UNIQUE INDEX domain_by_id_domain_idx ON domain_by_id(domain);
CREATE TABLE client_by_id (id INTEGER PRIMARY KEY, ip TEXT NOT NULL, name TEXT);
CREATE UNIQUE INDEX client_by_id_client_idx ON client_by_id(ip, name);
CREATE TABLE forward_by_id (id INTEGER PRIMARY KEY, forward TEXT NOT NULL);
CREATE TABLE addinfo_by_id (id INTEGER PRIMARY KEY, type INTEGER NOT NULL, content NOT NULL);
CREATE VIEW queries AS SELECT q.id, q.timestamp, q.type, q.status, COALESCE(d.domain, q.domain) AS domain,
	COALESCE(c.ip, q.client) AS client, COALESCE(f.forward, q.forward) AS forward,
	COALESCE(a.content, q.additional_info) AS additional_info, q.reply_type, q.reply_time, q.dnssec, q.list_id, q.ede
	FROM query_storage q LEFT JOIN domain_by_id d ON q.domain = d.id LEFT JOIN client_by_id c ON q.client = c.id
	LEFT JOIN forward_by_id f ON q.forward = f.id LEFT JOIN addinfo_by_id a ON q.additional_info = a.id;
CREATE TABLE network (id INTEGER PRIMARY KEY NOT NULL, hwaddr TEXT UNIQUE NOT NULL, interface TEXT NOT NULL,
	firstSeen INTEGER NOT NULL, lastQuery INTEGER NOT NULL, numQueries INTEGER NOT NULL, macVendor TEXT, aliasclient_id INTEGER);
CREATE TABLE network_addresses (network_id INTEGER NOT NULL, ip TEXT UNIQUE NOT NULL,
	lastSeen INTEGER NOT NULL DEFAULT (cast(strftime('%s', 'now') as int)), name TEXT, nameUpdated INTEGER);
`

// tailDomain picks one of 20,000 long-tail domains.
func tailDomain(r *rand.Rand) string {
	i := r.IntN(20000)
	return fmt.Sprintf("t%d.cdn%d.example-tail.net", i, i%40)
}

var ftlTypes = map[string]int{"A": 1, "AAAA": 2, "PTR": 6, "TXT": 7, "SRV": 4, "HTTPS": 16, "SVCB": 15}

// scaleHousehold builds about 1.5M lookups over 30 days from 25 clients:
// the demo household's devices (which carry realistic heartbeats and
// daily rhythms) cloned onto 25 addresses, with a quarter of the lookups
// moved to a long tail of 20,000 domains the knowledge base does not know.
func scaleHousehold(end time.Time) ([]model.DNSQuery, []model.Device) {
	h := demo.Generate(7, end, 30)
	r := rand.New(rand.NewPCG(3, 4))
	var (
		qs   []model.DNSQuery
		devs []model.Device
	)
	for c := range 25 {
		src := h.Devices[c%len(h.Devices)]
		ip := netip.AddrFrom4([4]byte{192, 168, 1, byte(100 + c)})
		if c == 24 {
			ip = netip.MustParseAddr("fd00::1:24") // an IPv6-only client
		}
		mac := fmt.Sprintf("02:00:00:00:00:%02x", c)
		devs = append(devs, model.Device{ID: "mac:" + mac, MAC: mac, IPs: []netip.Addr{ip}, Hostname: fmt.Sprintf("%s-%d", src.Hostname, c)})
		shift := time.Duration(r.IntN(600)) * time.Second
		for _, q := range h.DNS {
			if !slices.Contains(src.IPs, q.ClientIP) || q.Time.Add(shift).After(end) {
				continue
			}
			q.ClientIP, q.Time = ip, q.Time.Add(shift)
			if r.IntN(4) == 0 {
				q.Domain = tailDomain(r)
			}
			qs = append(qs, q)
			// A few extra lookups bring the total to about 1.5M.
			if r.IntN(100) < 6 {
				q.Time = q.Time.Add(2 * time.Second)
				q.Domain = tailDomain(r)
				qs = append(qs, q)
			}
		}
	}
	slices.SortStableFunc(qs, func(a, b model.DNSQuery) int { return a.Time.Compare(b.Time) })
	return qs, devs
}

// writeFTL appends qs to an FTL database in the v6 layout, in one
// transaction per 50k rows, as FTL's periodic flush would.
func writeFTL(tb testing.TB, db *sql.DB, qs []model.DNSQuery) {
	tb.Helper()
	domIDs, cliIDs := map[string]int64{}, map[string]int64{}
	id := func(table, col string, m map[string]int64, v string, tx *sql.Tx) int64 {
		if n, ok := m[v]; ok {
			return n
		}
		var n int64
		err := tx.QueryRow(`SELECT id FROM `+table+` WHERE `+col+` = ?`, v).Scan(&n)
		if err == sql.ErrNoRows {
			err = tx.QueryRow(`INSERT INTO `+table+`(`+col+`) VALUES (?) RETURNING id`, v).Scan(&n)
		}
		if err != nil {
			tb.Fatal(err)
		}
		m[v] = n
		return n
	}
	for len(qs) > 0 {
		chunk := qs[:min(50000, len(qs))]
		qs = qs[len(chunk):]
		tx, err := db.Begin()
		if err != nil {
			tb.Fatal(err)
		}
		ins, err := tx.Prepare(`INSERT INTO query_storage (timestamp, type, status, domain, client) VALUES (?, ?, ?, ?, ?)`)
		if err != nil {
			tb.Fatal(err)
		}
		for _, q := range chunk {
			status := 2 // FORWARDED
			if q.Blocked {
				status = 1 // GRAVITY
			}
			typ, ok := ftlTypes[q.QType]
			if !ok {
				typ = 1
			}
			ts := float64(q.Time.UnixMicro()) / 1e6
			if _, err := ins.Exec(ts, typ, status, id("domain_by_id", "domain", domIDs, q.Domain, tx),
				id("client_by_id", "ip", cliIDs, q.ClientIP.String(), tx)); err != nil {
				tb.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			tb.Fatal(err)
		}
	}
}

func writeNetwork(tb testing.TB, db *sql.DB, devs []model.Device, at time.Time) {
	tb.Helper()
	for i, d := range devs {
		if _, err := db.Exec(`INSERT INTO network (id, hwaddr, interface, firstSeen, lastQuery, numQueries) VALUES (?, ?, 'eth0', ?, ?, 0)`,
			i+1, d.MAC, at.Unix()-30*86400, at.Unix()); err != nil {
			tb.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO network_addresses (network_id, ip, lastSeen, name) VALUES (?, ?, ?, ?)`,
			i+1, d.IPs[0].String(), at.Unix(), d.Hostname); err != nil {
			tb.Fatal(err)
		}
	}
}

// heapPeak samples the live heap until stop is called and returns the
// highest value seen.
func heapPeak() (stop func() uint64) {
	var (
		peak atomic.Uint64
		done = make(chan struct{})
		wg   sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		var ms runtime.MemStats
		for {
			runtime.ReadMemStats(&ms)
			if ms.HeapAlloc > peak.Load() {
				peak.Store(ms.HeapAlloc)
			}
			select {
			case <-done:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	return func() uint64 {
		close(done)
		wg.Wait()
		return peak.Load()
	}
}

func fileSize(paths ...string) int64 {
	var n int64
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			n += fi.Size()
		}
	}
	return n
}

const mib = 1 << 20

// dnsRecords is how many lookups the DNS source has delivered so far.
func dnsRecords(t *testing.T, st *store.Store) int64 {
	t.Helper()
	s, err := st.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range s.Sources {
		if src.Kind == "dns" {
			return src.Records
		}
	}
	return 0
}

// TestScale ingests a month of a busy 25-device home from a Pi-hole v6
// database and times the reports. It takes about a minute and a few hundred
// MB, so it only runs with PHONEHOME_SCALE=1:
//
//	PHONEHOME_SCALE=1 go test -run TestScale -v -timeout 30m ./internal/ingest
//
// Set PHONEHOME_SCALE_DIR to keep the generated databases. docs/performance.md
// records the numbers.
func TestScale(t *testing.T) {
	if os.Getenv("PHONEHOME_SCALE") == "" || testing.Short() {
		t.Skip("set PHONEHOME_SCALE=1 to run the scale test")
	}
	dir := os.Getenv("PHONEHOME_SCALE_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	ctx := context.Background()
	end := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	start := time.Now()
	qs, devs := scaleHousehold(end)
	// Hold back the last hour for the incremental run.
	cut, _ := slices.BinarySearchFunc(qs, end.Add(-time.Hour), func(q model.DNSQuery, t time.Time) int { return q.Time.Compare(t) })
	ftlPath := filepath.Join(dir, "pihole-FTL.db")
	os.Remove(ftlPath)
	ftl, err := sql.Open("sqlite", ftlPath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer ftl.Close()
	if _, err := ftl.Exec(ftlSchema); err != nil {
		t.Fatal(err)
	}
	writeFTL(t, ftl, qs[:cut])
	writeNetwork(t, ftl, devs, end)
	t.Logf("generated FTL database: %d lookups, %d clients, %.0f MiB, in %v",
		len(qs), len(devs), float64(fileSize(ftlPath, ftlPath+"-wal"))/mib, time.Since(start).Round(time.Millisecond))
	// Let go of the generated lookups so heap figures show phonehome only.
	total, rest := len(qs), slices.Clone(qs[cut:])
	qs = nil

	stPath := filepath.Join(dir, "phonehome.db")
	for _, p := range []string{stPath, stPath + "-wal", stPath + "-shm"} {
		os.Remove(p)
	}
	st, err := store.Open(stPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	src, err := pihole.NewDB(ftlPath)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	r := &Runner{
		Store: st, DNS: []source.DNSSource{src}, Devices: []source.DeviceSource{src},
		Retention: 90 * 24 * time.Hour, Logger: quiet, Now: func() time.Time { return end },
	}

	// First ingest: RunOnce caps each source's work per run, so repeat
	// until the backlog is gone, as successive ticks of `serve` would.
	runtime.GC()
	stop := heapPeak()
	start = time.Now()
	runs := 0
	for {
		runs++
		if err := r.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if dnsRecords(t, st) >= int64(cut) || runs > 100 {
			break
		}
	}
	elapsed := time.Since(start)
	peak := stop()
	t.Logf("first ingest: %d lookups in %v over %d runs (%.0f lookups/s), heap peak %.0f MiB",
		cut, elapsed.Round(time.Millisecond), runs, float64(cut)/elapsed.Seconds(), float64(peak)/mib)

	// Incremental: FTL flushes the last hour, then one ingest run.
	writeFTL(t, ftl, rest)
	start = time.Now()
	if err := r.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("incremental ingest: %d lookups in %v", len(rest), time.Since(start).Round(time.Millisecond))
	if n := dnsRecords(t, st); n != int64(total) {
		t.Fatalf("stored %d lookups, want %d", n, total)
	}

	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("store on disk: %.1f MiB (%.1f B/lookup)",
		float64(fileSize(stPath, stPath+"-wal"))/mib, float64(fileSize(stPath, stPath+"-wal"))/float64(total))
	if st, err = store.Open(stPath); err != nil {
		t.Fatal(err)
	}

	k := kb.Default()
	for _, days := range []int{1, 7, 30} {
		p := model.Period{From: end.Add(-time.Duration(days) * 24 * time.Hour), To: end}
		runtime.GC()
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		stop := heapPeak()
		start := time.Now()
		ds, err := st.Devices(ctx)
		if err != nil {
			t.Fatal(err)
		}
		dq, err := st.DNSBetween(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		fl, err := st.FlowsBetween(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		read := time.Since(start)
		runtime.ReadMemStats(&m1)
		readAlloc := m1.TotalAlloc - m0.TotalAlloc
		runtime.GC()
		runtime.ReadMemStats(&m1)
		live := m1.HeapAlloc - m0.HeapAlloc // the lookups just read
		start = time.Now()
		rep := analyze.Analyze(k, p, ds, dq, fl, analyze.DefaultOptions())
		analyzed := time.Since(start)
		peak := stop()
		t.Logf("report days=%d: %d lookups, %d devices; read %v (allocates %.0f MiB, keeps %.0f MiB) + analyze %v; heap peak %.0f MiB",
			days, len(dq), len(rep.Devices), read.Round(time.Millisecond), float64(readAlloc)/mib, float64(live)/mib,
			analyzed.Round(time.Millisecond), float64(peak)/mib)
	}

	// Retention: drop the oldest week, as a 23-day retention would.
	start = time.Now()
	n, err := st.Prune(ctx, end.Add(-23*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("prune: %d lookups older than 23 days in %v", n, time.Since(start).Round(time.Millisecond))
}
