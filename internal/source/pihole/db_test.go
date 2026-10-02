package pihole

import (
	"context"
	"database/sql"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/source"
)

var (
	_ source.DNSSource    = (*DB)(nil)
	_ source.DeviceSource = (*DB)(nil)
)

// v5Schema is FTL's schema before query_storage existed (Pi-hole v5.0–5.7):
// a plain queries table with integer timestamps.
const v5Schema = `
CREATE TABLE queries (id INTEGER PRIMARY KEY AUTOINCREMENT, timestamp INTEGER NOT NULL, type INTEGER NOT NULL,
	status INTEGER NOT NULL, domain TEXT NOT NULL, client TEXT NOT NULL, forward TEXT, additional_info TEXT);
CREATE TABLE network (id INTEGER PRIMARY KEY NOT NULL, hwaddr TEXT UNIQUE NOT NULL, interface TEXT NOT NULL,
	firstSeen INTEGER NOT NULL, lastQuery INTEGER NOT NULL, numQueries INTEGER NOT NULL, macVendor TEXT, aliasclient_id INTEGER);
CREATE TABLE network_addresses (network_id INTEGER NOT NULL, ip TEXT UNIQUE NOT NULL,
	lastSeen INTEGER NOT NULL DEFAULT (cast(strftime('%s', 'now') as int)), name TEXT, nameUpdated INTEGER);
`

// v6Schema is current FTL's: ids in query_storage, strings in *_by_id
// tables, a queries view joining them, and fractional timestamps.
const v6Schema = `
CREATE TABLE query_storage (id INTEGER PRIMARY KEY AUTOINCREMENT, timestamp INTEGER NOT NULL, type INTEGER NOT NULL,
	status INTEGER NOT NULL, domain INTEGER NOT NULL, client INTEGER NOT NULL, forward INTEGER, additional_info INTEGER,
	reply_type INTEGER, reply_time REAL, dnssec INTEGER, list_id INTEGER, ede INTEGER);
CREATE TABLE domain_by_id (id INTEGER PRIMARY KEY, domain TEXT NOT NULL);
CREATE TABLE client_by_id (id INTEGER PRIMARY KEY, ip TEXT NOT NULL, name TEXT);
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
INSERT INTO domain_by_id VALUES (1, 'Acr.SamsungCloudSolution.com.'), (2, 'example.org'), (3, 'ads.example.net');
INSERT INTO client_by_id VALUES (1, '192.168.1.20', 'tv'), (2, 'fe80::1%eth0', NULL), (3, 'not-an-ip', NULL);
`

// fixture creates a database with the given schema in a temporary
// directory and returns its path and a writable handle standing in for FTL.
func fixture(t *testing.T, schema string) (string, *sql.DB) {
	t.Helper()
	// Awkward directory names must survive the file: URI.
	dir := filepath.Join(t.TempDir(), "etc pihole #1 %20")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return fixtureAt(t, filepath.Join(dir, "pihole-FTL.db"), schema)
}

// fixtureAt is fixture with a given path.
func fixtureAt(t *testing.T, path, schema string) (string, *sql.DB) {
	t.Helper()
	w, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	if _, err := w.Exec(schema); err != nil {
		t.Fatal(err)
	}
	return path, w
}

func exec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

func openDB(t *testing.T, path string) *DB {
	t.Helper()
	d, err := NewDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestDBFetchV5(t *testing.T) {
	path, w := fixture(t, v5Schema)
	exec(t, w, `INSERT INTO queries (timestamp, type, status, domain, client) VALUES
		(1727870281, 1, 2, 'Example.ORG.', '192.168.1.20'),
		(1727870282, 2, 1, 'ads.example.net', '192.168.1.20'),
		(1727870283, 165, 3, 'svc.example.org', '::ffff:192.168.1.21'),
		(1727870284, 16, 9, 'cname.example.net', '192.168.1.22'),
		(1727870285, 1, 4, 'regex.example.net', 'garbage')`)
	d := openDB(t, path)
	ctx := context.Background()

	got, cur, err := d.FetchDNS(ctx, "", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.DNSQuery{
		{Time: time.Unix(1727870281, 0), ClientIP: netip.MustParseAddr("192.168.1.20"), Domain: "example.org", QType: "A", Source: "pihole"},
		{Time: time.Unix(1727870282, 0), ClientIP: netip.MustParseAddr("192.168.1.20"), Domain: "ads.example.net", QType: "AAAA", Blocked: true, Source: "pihole"},
		{Time: time.Unix(1727870283, 0), ClientIP: netip.MustParseAddr("192.168.1.21"), Domain: "svc.example.org", QType: "TYPE65", Source: "pihole"},
	}
	assertQueries(t, got, want)
	if cur != "3" {
		t.Fatalf("cursor = %q, want 3", cur)
	}

	// The unparsable client is skipped but still consumed.
	got, cur, err = d.FetchDNS(ctx, cur, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertQueries(t, got, []model.DNSQuery{
		{Time: time.Unix(1727870284, 0), ClientIP: netip.MustParseAddr("192.168.1.22"), Domain: "cname.example.net", QType: "HTTPS", Blocked: true, Source: "pihole"},
	})
	if cur != "5" {
		t.Fatalf("cursor = %q, want 5", cur)
	}

	got, cur2, err := d.FetchDNS(ctx, cur, 10)
	if err != nil || got != nil || cur2 != cur {
		t.Fatalf("nothing new: got %v, %q, %v", got, cur2, err)
	}

	// FTL keeps writing while we hold the database open.
	exec(t, w, `INSERT INTO queries (timestamp, type, status, domain, client) VALUES (1727870290, 1, 5, 'deny.example', '192.168.1.20')`)
	got, cur, err = d.FetchDNS(ctx, cur, 10)
	if err != nil || len(got) != 1 || !got[0].Blocked || cur != "6" {
		t.Fatalf("after concurrent write: got %v, %q, %v", got, cur, err)
	}
}

func TestDBFetchV6(t *testing.T) {
	path, w := fixture(t, v6Schema)
	exec(t, w, `INSERT INTO query_storage (timestamp, type, status, domain, client) VALUES
		(1727870281.25, 1, 2, 1, 1),
		(1727870281.5, 2, 18, 3, 2),
		(1727870282.75, 1, 3, 2, 3),
		(1727870283.125, 7, 17, 2, 1)`)
	d := openDB(t, path)

	got, cur, err := d.FetchDNS(context.Background(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	assertQueries(t, got, []model.DNSQuery{
		{Time: time.Unix(1727870281, 250e6), ClientIP: netip.MustParseAddr("192.168.1.20"), Domain: "acr.samsungcloudsolution.com", QType: "A", Source: "pihole"},
		{Time: time.Unix(1727870281, 500e6), ClientIP: netip.MustParseAddr("fe80::1"), Domain: "ads.example.net", QType: "AAAA", Blocked: true, Source: "pihole"},
		{Time: time.Unix(1727870283, 125e6), ClientIP: netip.MustParseAddr("192.168.1.20"), Domain: "example.org", QType: "TXT", Source: "pihole"},
	})
	if cur != "4" {
		t.Fatalf("cursor = %q, want 4", cur)
	}
}

func TestDBErrors(t *testing.T) {
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "nope.db")
	d := openDB(t, missing)
	if _, _, err := d.FetchDNS(ctx, "", 10); err == nil {
		t.Fatal("expected error for missing database")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("read-only open created %s", missing)
	}

	path, _ := fixture(t, v5Schema)
	d = openDB(t, path)
	if _, _, err := d.FetchDNS(ctx, "x", 10); err == nil {
		t.Fatal("expected error for bad cursor")
	}
	if _, err := d.db.ExecContext(ctx, `DELETE FROM queries`); err == nil {
		t.Fatal("database is writable")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := d.FetchDNS(cctx, "", 10); err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestDBDevices(t *testing.T) {
	for name, schema := range map[string]string{"v5": v5Schema, "v6": v6Schema} {
		t.Run(name, func(t *testing.T) {
			path, w := fixture(t, schema)
			exec(t, w, `INSERT INTO network (id, hwaddr, interface, firstSeen, lastQuery, numQueries, macVendor) VALUES
				(1, 'AA:BB:CC:00:11:22', 'eth0', 1727800000, 1727870000, 10, 'Samsung Electronics Co.,Ltd'),
				(2, 'ip-192.168.1.50', 'N/A', 1727800100, 0, 3, NULL),
				(3, '00:00:00:00:00:00', 'lo', 1727800000, 1727870000, 1, 'virtual interface'),
				(4, 'ip-bogus', 'N/A', 1727800000, 0, 1, NULL)`)
			exec(t, w, `INSERT INTO network_addresses (network_id, ip, lastSeen, name) VALUES
				(1, '192.168.1.20', 1727870100, 'samsung-tv.lan'),
				(1, '192.168.1.19', 1727000000, 'old-name'),
				(1, 'fe80::aabb:ccff:fe00:1122', 1727860000, NULL),
				(2, '192.168.1.50', 1727870200, NULL),
				(3, '127.0.0.1', 1727870000, 'localhost')`)

			got, err := openDB(t, path).Devices(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := []model.Device{
				{
					ID: "ip:192.168.1.50", IPs: []netip.Addr{netip.MustParseAddr("192.168.1.50")},
					FirstSeen: time.Unix(1727800100, 0), LastSeen: time.Unix(1727870200, 0),
				},
				{
					ID: "mac:aa:bb:cc:00:11:22", MAC: "aa:bb:cc:00:11:22",
					IPs: []netip.Addr{
						netip.MustParseAddr("192.168.1.20"),
						netip.MustParseAddr("fe80::aabb:ccff:fe00:1122"),
						netip.MustParseAddr("192.168.1.19"),
					},
					Hostname: "samsung-tv.lan", Vendor: "Samsung Electronics Co.,Ltd",
					FirstSeen: time.Unix(1727800000, 0), LastSeen: time.Unix(1727870100, 0),
				},
			}
			if len(got) != len(want) {
				t.Fatalf("got %d devices, want %d: %+v", len(got), len(want), got)
			}
			for i := range want {
				assertDevice(t, got[i], want[i])
			}
		})
	}
}

func assertQueries(t *testing.T, got, want []model.DNSQuery) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d queries, want %d:\n%+v", len(got), len(want), got)
	}
	for i := range want {
		g, w := got[i], want[i]
		if !g.Time.Equal(w.Time) || g.ClientIP != w.ClientIP || g.Domain != w.Domain ||
			g.QType != w.QType || g.Blocked != w.Blocked || g.Source != w.Source {
			t.Errorf("query %d:\n got %+v\nwant %+v", i, g, w)
		}
	}
}

func assertDevice(t *testing.T, g, w model.Device) {
	t.Helper()
	ok := g.ID == w.ID && g.MAC == w.MAC && g.Hostname == w.Hostname && g.Vendor == w.Vendor &&
		g.FirstSeen.Equal(w.FirstSeen) && g.LastSeen.Equal(w.LastSeen) && len(g.IPs) == len(w.IPs)
	for i := 0; ok && i < len(w.IPs); i++ {
		ok = g.IPs[i] == w.IPs[i]
	}
	if !ok {
		t.Errorf("device:\n got %+v\nwant %+v", g, w)
	}
}
