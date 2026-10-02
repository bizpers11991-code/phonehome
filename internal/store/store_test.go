package store

import (
	"context"
	"fmt"
	"net/netip"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func openMem(t testing.TB) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func q(at time.Duration, client, domain string) model.DNSQuery {
	return model.DNSQuery{
		Time:     t0.Add(at),
		ClientIP: netip.MustParseAddr(client),
		Domain:   domain,
		QType:    "A",
		Source:   "pihole",
	}
}

func period(from, to time.Duration) model.Period {
	return model.Period{From: t0.Add(from), To: t0.Add(to)}
}

func TestOpenMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "phonehome.db")
	for range 2 { // second Open finds the schema current
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var v int
		if err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		if v != len(migrations) {
			t.Errorf("schema_version = %d, want %d", v, len(migrations))
		}
		var mode string
		if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
			t.Errorf("journal_mode = %q, %v; want wal", mode, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "phonehome.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE meta SET value = 999 WHERE key = 'schema_version'`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("Open of a newer schema succeeded")
	}
}

func TestOpenPathWithSpecialCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "my data?#%", "phone home.db")
	if _, err := Open(path); err == nil {
		t.Fatal("Open in a missing directory succeeded")
	}
	path = filepath.Join(t.TempDir(), "phone home?#%.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	st, err := s.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.DBPath != path {
		t.Errorf("DBPath = %q, want %q", st.DBPath, path)
	}
}

func TestDNSRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	v6 := q(2*time.Second, "fd00::1", "api.example.com")
	v6.QType, v6.Blocked, v6.Source = "AAAA", true, "adguard"
	noIP := q(3*time.Second, "10.0.0.1", "")
	noIP.ClientIP, noIP.QType = netip.Addr{}, ""
	in := []model.DNSQuery{
		q(time.Second, "192.168.1.10", "example.com"),
		v6,
		noIP,
		q(time.Second, "192.168.1.11", "example.com"), // same time as the first
		q(-time.Second, "192.168.1.10", "early.example"),
		q(time.Hour, "192.168.1.10", "late.example"),
	}
	if err := s.InsertDNS(ctx, in[:3]); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertDNS(ctx, in[3:]); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertDNS(ctx, nil); err != nil {
		t.Fatal(err)
	}

	got, err := s.DNSBetween(ctx, period(0, time.Hour)) // half-open: excludes in[5]
	if err != nil {
		t.Fatal(err)
	}
	want := []model.DNSQuery{in[0], in[3], in[1], in[2]}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DNSBetween:\n got %+v\nwant %+v", got, want)
	}
	if got, _ := s.DNSBetween(ctx, period(2*time.Hour, 3*time.Hour)); len(got) != 0 {
		t.Errorf("empty period returned %d rows", len(got))
	}
}

// TestDNSMultiRowInsert checks batches around the rows-per-statement size,
// and that a period starting after a domain's earlier lookups still finds
// its later ones.
func TestDNSMultiRowInsert(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	var in []model.DNSQuery
	for _, n := range []int{1, insertRows - 1, insertRows, insertRows + 1, 2*insertRows + 3} {
		var batch []model.DNSQuery
		for range n {
			i := len(in) + len(batch)
			batch = append(batch, q(time.Duration(i)*time.Second, "192.168.1.10", fmt.Sprintf("d%d.example", i%7)))
		}
		if err := s.InsertDNS(ctx, batch); err != nil {
			t.Fatal(err)
		}
		in = append(in, batch...)
	}
	got, err := s.DNSBetween(ctx, period(0, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("got %d lookups, want %d, or they differ", len(got), len(in))
	}
	got, err = s.DNSBetween(ctx, period(100*time.Second, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, in[100:]) {
		t.Fatalf("got %d lookups from 100s, want %d", len(got), len(in)-100)
	}
}

func TestDNSUnknownDomainCacheAcrossBatches(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	s.domains.max = 2 // force the cache to reset mid-stream
	var in []model.DNSQuery
	for i := range 20 {
		in = append(in, q(time.Duration(i)*time.Second, "192.168.1.10", fmt.Sprintf("d%d.example", i%5)))
	}
	for i := 0; i < len(in); i += 3 {
		if err := s.InsertDNS(ctx, in[i:min(i+3, len(in))]); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.DNSBetween(ctx, period(0, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("DNSBetween:\n got %+v\nwant %+v", got, in)
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM domains`).Scan(&n)
	if n != 5 {
		t.Errorf("%d domain rows, want 5", n)
	}
}

func TestInsertDNSRollbackLeavesCacheClean(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.InsertDNS(cctx, []model.DNSQuery{q(0, "10.0.0.1", "a.example")}); err == nil {
		t.Fatal("InsertDNS with cancelled context succeeded")
	}
	if len(s.domains.m) != 0 {
		t.Errorf("cache holds %v after failed insert", s.domains.m)
	}
	if err := s.InsertDNS(ctx, []model.DNSQuery{q(0, "10.0.0.1", "a.example")}); err != nil {
		t.Fatal(err)
	}
}

func TestFlowsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	in := []model.Flow{
		{
			Start: t0.Add(time.Minute), End: t0.Add(2 * time.Minute),
			ClientIP: netip.MustParseAddr("192.168.1.10"), RemoteIP: netip.MustParseAddr("2001:db8::1"),
			RemotePort: 443, Proto: "tcp", BytesOut: 1 << 63, BytesIn: 42, Source: "conntrack",
		},
		{
			Start:    t0,
			ClientIP: netip.MustParseAddr("192.168.1.11"), RemoteIP: netip.MustParseAddr("8.8.8.8"),
			RemotePort: 53, Proto: "udp", Source: "conntrack",
		},
		{Start: t0.Add(time.Hour), Source: "conntrack"},
	}
	if err := s.InsertFlows(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := s.FlowsBetween(ctx, period(0, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Flow{in[1], in[0]}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FlowsBetween:\n got %+v\nwant %+v", got, want)
	}
}

func TestMergeDevice(t *testing.T) {
	early, late := t0, t0.Add(time.Hour)
	stored := model.Device{
		ID: "mac:aa", MAC: "aa", Hostname: "old-host", Vendor: "", Kind: model.KindTV,
		Label: "Living room", FirstSeen: early, LastSeen: early,
	}
	tests := []struct {
		name string
		old  model.Device
		in   model.Device
		want model.Device
	}{
		{
			name: "new device",
			old:  model.Device{ID: "mac:aa"},
			in:   model.Device{ID: "mac:aa", Hostname: "tv", Kind: model.KindTV, FirstSeen: early, LastSeen: late},
			want: model.Device{ID: "mac:aa", Hostname: "tv", Kind: model.KindTV, FirstSeen: early, LastSeen: late},
		},
		{
			name: "older observation fills only empty fields",
			old:  stored,
			in: model.Device{ID: "mac:aa", Hostname: "stale", Vendor: "Samsung", Kind: model.KindSpeaker,
				FirstSeen: early.Add(-time.Hour), LastSeen: early.Add(-time.Minute)},
			want: model.Device{ID: "mac:aa", MAC: "aa", Hostname: "old-host", Vendor: "Samsung", Kind: model.KindTV,
				Label: "Living room", FirstSeen: early.Add(-time.Hour), LastSeen: early},
		},
		{
			name: "same LastSeen is not newer",
			old:  stored,
			in:   model.Device{ID: "mac:aa", Hostname: "other", LastSeen: early},
			want: stored,
		},
		{
			name: "newer observation overwrites",
			old:  stored,
			in:   model.Device{ID: "mac:aa", Hostname: "new-host", Kind: model.KindStreamer, Label: "ignored", LastSeen: late},
			want: model.Device{ID: "mac:aa", MAC: "aa", Hostname: "new-host", Kind: model.KindStreamer,
				Label: "Living room", FirstSeen: early, LastSeen: late},
		},
		{
			name: "empty and unknown never overwrite",
			old:  stored,
			in:   model.Device{ID: "mac:aa", Kind: model.KindUnknown, LastSeen: late},
			want: model.Device{ID: "mac:aa", MAC: "aa", Hostname: "old-host", Kind: model.KindTV,
				Label: "Living room", FirstSeen: early, LastSeen: late},
		},
		{
			name: "stored unknown kind is replaced",
			old:  model.Device{ID: "mac:aa", Kind: model.KindUnknown, LastSeen: late},
			in:   model.Device{ID: "mac:aa", Kind: model.KindCamera, LastSeen: early},
			want: model.Device{ID: "mac:aa", Kind: model.KindCamera, LastSeen: late},
		},
		{
			name: "zero FirstSeen is ignored",
			old:  stored,
			in:   model.Device{ID: "mac:aa"},
			want: stored,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeDevice(tt.old, tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mergeDevice:\n got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestDevices(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	ip := netip.MustParseAddr
	if err := s.SetLabel(ctx, "mac:bb", "Kitchen speaker"); err != nil { // before it is seen
		t.Fatal(err)
	}
	err := s.UpsertDevices(ctx, []model.Device{
		{ID: "mac:aa", MAC: "aa", Hostname: "tv", IPs: []netip.Addr{ip("192.168.1.10"), {}}, FirstSeen: t0, LastSeen: t0},
		{ID: "mac:bb", MAC: "bb", Vendor: "Amazon", Label: "ignored", IPs: []netip.Addr{ip("192.168.1.20")}, FirstSeen: t0, LastSeen: t0},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Later: aa moves to .11, and DHCP hands .10 to bb.
	err = s.UpsertDevices(ctx, []model.Device{
		{ID: "mac:aa", IPs: []netip.Addr{ip("192.168.1.11")}, LastSeen: t0.Add(time.Hour)},
		{ID: "mac:bb", IPs: []netip.Addr{ip("192.168.1.10")}, LastSeen: t0.Add(2 * time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	// A stale report must not move .10 back to aa.
	if err := s.UpsertDevices(ctx, []model.Device{{ID: "mac:aa", IPs: []netip.Addr{ip("192.168.1.10")}, LastSeen: t0}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLabel(ctx, "mac:aa", "TV"); err != nil {
		t.Fatal(err)
	}

	got, err := s.Devices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Device{
		{ID: "mac:aa", MAC: "aa", Hostname: "tv", Label: "TV", IPs: []netip.Addr{ip("192.168.1.11")},
			FirstSeen: t0, LastSeen: t0.Add(time.Hour)},
		{ID: "mac:bb", MAC: "bb", Vendor: "Amazon", Label: "Kitchen speaker",
			IPs: []netip.Addr{ip("192.168.1.10"), ip("192.168.1.20")}, FirstSeen: t0, LastSeen: t0.Add(2 * time.Hour)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Devices:\n got %+v\nwant %+v", got, want)
	}

	if err := s.UpsertDevices(ctx, []model.Device{{}}); err == nil {
		t.Error("UpsertDevices accepted an empty ID")
	}
	if err := s.SetLabel(ctx, "", "x"); err == nil {
		t.Error("SetLabel accepted an empty ID")
	}
}

func TestCursorPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "phonehome.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if c, err := s.Cursor(ctx, "pihole"); err != nil || c != "" {
		t.Fatalf("Cursor of new source = %q, %v", c, err)
	}
	for _, c := range []string{"100", "200"} {
		if err := s.SetCursor(ctx, "pihole", c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if c, err := s.Cursor(ctx, "pihole"); err != nil || c != "200" {
		t.Errorf("Cursor after reopen = %q, %v; want 200", c, err)
	}
}

func TestRecordRunAndStatus(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	st, err := s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := (model.Status{DBPath: ":memory:"}); !reflect.DeepEqual(st, want) {
		t.Errorf("empty Status = %+v", st)
	}

	runs := []model.SourceStatus{
		{Name: "pihole", Kind: "dns", LastRun: t0, LastOK: t0, Records: 10},
		{Name: "pihole", LastRun: t0.Add(time.Minute), LastOK: t0.Add(time.Minute), Records: 5, LastError: "boom"},
		{Name: "leases", Kind: "devices", LastRun: t0, LastError: "no such file"},
	}
	for _, r := range runs {
		if err := s.RecordRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordRun(ctx, model.SourceStatus{}); err == nil {
		t.Error("RecordRun accepted an empty name")
	}
	if err := s.UpsertDevices(ctx, []model.Device{{ID: "mac:aa"}, {ID: "mac:bb"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertDNS(ctx, []model.DNSQuery{q(time.Hour, "10.0.0.1", "b"), q(-time.Hour, "10.0.0.1", "a")}); err != nil {
		t.Fatal(err)
	}

	st, err = s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := model.Status{
		DBPath: ":memory:",
		Sources: []model.SourceStatus{
			{Name: "leases", Kind: "devices", LastRun: t0, LastError: "no such file"},
			{Name: "pihole", Kind: "dns", LastRun: t0.Add(time.Minute), LastOK: t0, Records: 15, LastError: "boom"},
		},
		Devices: 2,
		Oldest:  t0.Add(-time.Hour),
		Newest:  t0.Add(time.Hour),
	}
	if !reflect.DeepEqual(st, want) {
		t.Errorf("Status:\n got %+v\nwant %+v", st, want)
	}

	// A successful run clears the error and moves LastOK.
	if err := s.RecordRun(ctx, model.SourceStatus{Name: "pihole", LastRun: t0.Add(2 * time.Minute), LastOK: t0.Add(2 * time.Minute), Records: 1}); err != nil {
		t.Fatal(err)
	}
	st, _ = s.Status(ctx)
	if got := st.Sources[1]; got.LastError != "" || !got.LastOK.Equal(t0.Add(2*time.Minute)) || got.Records != 16 || got.Kind != "dns" {
		t.Errorf("after successful run: %+v", got)
	}
}

func TestPrune(t *testing.T) {
	ctx := context.Background()
	s := openMem(t)
	var in []model.DNSQuery
	for i := range pruneChunk*2 + 7 { // several chunks
		in = append(in, q(time.Duration(i)*time.Millisecond-time.Hour, "10.0.0.1", "old.example"))
	}
	in = append(in,
		q(-time.Minute, "10.0.0.1", "shared.example"),
		q(time.Minute, "10.0.0.1", "shared.example"),
		q(time.Minute, "10.0.0.1", "new.example"),
		q(0, "10.0.0.1", "boundary.example"), // exactly at the cut: kept
	)
	if err := s.InsertDNS(ctx, in); err != nil {
		t.Fatal(err)
	}
	flows := []model.Flow{{Start: t0.Add(-time.Second), Source: "ct"}, {Start: t0, Source: "ct"}}
	if err := s.InsertFlows(ctx, flows); err != nil {
		t.Fatal(err)
	}

	n, err := s.Prune(ctx, t0)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(pruneChunk*2 + 7 + 1 + 1); n != want {
		t.Errorf("Prune deleted %d rows, want %d", n, want)
	}
	got, err := s.DNSBetween(ctx, period(-48*time.Hour, 48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if want := []model.DNSQuery{in[len(in)-1], in[len(in)-3], in[len(in)-2]}; !reflect.DeepEqual(got, want) {
		t.Errorf("after Prune:\n got %+v\nwant %+v", got, want)
	}
	fl, _ := s.FlowsBetween(ctx, period(-48*time.Hour, 48*time.Hour))
	if !reflect.DeepEqual(fl, flows[1:]) {
		t.Errorf("flows after Prune: %+v", fl)
	}
	var names []string
	rows, _ := s.db.Query(`SELECT name FROM domains ORDER BY name`)
	for rows.Next() {
		var n string
		rows.Scan(&n)
		names = append(names, n)
	}
	if want := []string{"boundary.example", "new.example", "shared.example"}; !reflect.DeepEqual(names, want) {
		t.Errorf("domains after Prune = %v, want %v", names, want)
	}

	// Domains cached before the prune must be re-resolved, not reused.
	if err := s.InsertDNS(ctx, []model.DNSQuery{q(2*time.Minute, "10.0.0.1", "old.example")}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.DNSBetween(ctx, period(2*time.Minute, 3*time.Minute))
	if len(got) != 1 || got[0].Domain != "old.example" {
		t.Errorf("re-inserted pruned domain: %+v", got)
	}
	if n, err := s.Prune(ctx, t0); err != nil || n != 0 {
		t.Errorf("second Prune = %d, %v; want 0", n, err)
	}
}

// TestPruneFromAnotherProcess checks that a Store notices domains deleted by
// a different handle on the same file and does not reuse stale cached ids.
func TestPruneFromAnotherProcess(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "phonehome.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if err := a.InsertDNS(ctx, []model.DNSQuery{q(-time.Hour, "10.0.0.1", "gone.example")}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Prune(ctx, t0); err != nil {
		t.Fatal(err)
	}
	// Reuses gone.example's id for a different name if ids are recycled.
	if err := b.InsertDNS(ctx, []model.DNSQuery{q(time.Minute, "10.0.0.1", "other.example")}); err != nil {
		t.Fatal(err)
	}
	if err := a.InsertDNS(ctx, []model.DNSQuery{q(2*time.Minute, "10.0.0.1", "gone.example")}); err != nil {
		t.Fatal(err)
	}
	got, err := a.DNSBetween(ctx, period(0, time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Domain != "other.example" || got[1].Domain != "gone.example" {
		t.Errorf("DNSBetween = %+v", got)
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "phonehome.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const batches, size = 50, 100
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	written := make(chan struct{})
	wg.Go(func() {
		defer close(written)
		for b := range batches {
			var qs []model.DNSQuery
			for i := range size {
				qs = append(qs, q(time.Duration(b*size+i)*time.Second, "10.0.0.1", fmt.Sprintf("d%d.example", i%37)))
			}
			if err := s.InsertDNS(ctx, qs); err != nil {
				errs <- err
				return
			}
		}
	})
	wg.Go(func() {
		for b := range batches {
			dev := model.Device{ID: "mac:aa", IPs: []netip.Addr{netip.AddrFrom4([4]byte{10, 0, 0, byte(b)})}, LastSeen: t0.Add(time.Duration(b))}
			if err := s.UpsertDevices(ctx, []model.Device{dev}); err != nil {
				errs <- err
				return
			}
			if err := s.RecordRun(ctx, model.SourceStatus{Name: "pihole", Records: 1}); err != nil {
				errs <- err
				return
			}
		}
	})
	for range 3 {
		wg.Go(func() {
			for last := 0; last < batches*size; {
				select {
				case <-written:
					return // the final state is checked below
				default:
				}
				got, err := s.DNSBetween(ctx, period(0, 24*time.Hour))
				if err != nil {
					errs <- err
					return
				}
				// Each batch commits atomically and readers never go backwards.
				if len(got) < last || len(got)%size != 0 {
					errs <- fmt.Errorf("reader saw %d rows after %d", len(got), last)
					return
				}
				last = len(got)
				if _, err := s.Devices(ctx); err != nil {
					errs <- err
					return
				}
				if _, err := s.Status(ctx); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got, err := s.DNSBetween(ctx, period(0, 24*time.Hour)); err != nil || len(got) != batches*size {
		t.Errorf("final DNSBetween = %d rows, %v; want %d", len(got), err, batches*size)
	}
}
