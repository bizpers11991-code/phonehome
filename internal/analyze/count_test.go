package analyze

import (
	"math/rand/v2"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

func qt(at time.Duration, client, domain, qtype string, blocked bool) model.DNSQuery {
	x := q(at, client, domain)
	x.QType, x.Blocked = qtype, blocked
	return x
}

// domainStat returns d's entry for domain, failing if there is none.
func domainStat(t *testing.T, d model.DeviceReport, domain string) model.DomainStat {
	t.Helper()
	for _, ds := range d.Domains {
		if ds.Domain == domain {
			return ds
		}
	}
	t.Fatalf("%s not in %s's domains", domain, d.Device.ID)
	return model.DomainStat{}
}

func TestRecordTypesCountOnce(t *testing.T) {
	at := 10 * time.Hour
	qs := []model.DNSQuery{
		qt(at, "10.0.0.5", "track.google.example", "A", false),
		qt(at+40*time.Millisecond, "10.0.0.5", "track.google.example", "AAAA", false),
		qt(at+90*time.Millisecond, "10.0.0.5", "track.google.example", "HTTPS", false),
	}
	d := Analyze(newFakeKB(), week, nil, qs, nil, utcOpt).Devices[0]
	if d.Total != 1 || d.Snooping != 1 || d.ByCategory[model.CatTracking] != 1 || d.Hourly[10] != 1 {
		t.Errorf("Total/Snooping/tracking/hour 10 = %d/%d/%d/%d, want 1 each",
			d.Total, d.Snooping, d.ByCategory[model.CatTracking], d.Hourly[10])
	}
	ds := domainStat(t, d, "track.google.example")
	if ds.Count != 1 || !ds.First.Equal(t0.Add(at)) || !ds.Last.Equal(t0.Add(at+90*time.Millisecond)) {
		t.Errorf("domain = %+v, want 1 lookup seen from the first query to the last", ds)
	}
}

func TestBlockedRequeriesCountOncePerWindow(t *testing.T) {
	// Pi-hole answers blocked names with a 2-second TTL; a device retrying
	// for ten minutes queries 300 times.
	var qs []model.DNSQuery
	for at := time.Duration(0); at < 10*time.Minute; at += 2 * time.Second {
		qs = append(qs, qt(time.Hour+at, "10.0.0.5", "ads.samsung.example", "A", true))
	}
	const want = 10 // one a minute
	if len(qs) != 300 {
		t.Fatalf("%d queries, want 300", len(qs))
	}
	d := Analyze(newFakeKB(), week, nil, qs, nil, utcOpt).Devices[0]
	if d.Total != want || d.Blocked != want || d.Snooping != want {
		t.Errorf("Total/Blocked/Snooping = %d/%d/%d, want %d each", d.Total, d.Blocked, d.Snooping, want)
	}
	if ds := domainStat(t, d, "ads.samsung.example"); ds.Count != want || ds.Blocked != want {
		t.Errorf("domain Count/Blocked = %d/%d, want %d", ds.Count, ds.Blocked, want)
	}
}

func TestBlockedIfAnyQueryWas(t *testing.T) {
	qs := []model.DNSQuery{
		qt(time.Hour, "10.0.0.5", "ads.samsung.example", "A", false),
		qt(time.Hour+time.Second, "10.0.0.5", "ads.samsung.example", "AAAA", true),
		qt(time.Hour+2*time.Second, "10.0.0.5", "ads.samsung.example", "HTTPS", true),
	}
	d := Analyze(newFakeKB(), week, nil, qs, nil, utcOpt).Devices[0]
	if ds := domainStat(t, d, "ads.samsung.example"); d.Total != 1 || d.Blocked != 1 || ds.Blocked != 1 {
		t.Errorf("Total/Blocked/domain Blocked = %d/%d/%d, want 1/1/1", d.Total, d.Blocked, ds.Blocked)
	}
}

func TestLookupsApartCountSeparately(t *testing.T) {
	for _, tc := range []struct {
		apart time.Duration
		want  int
	}{
		{61 * time.Second, 2},
		{60 * time.Second, 2},
		{59 * time.Second, 1},
	} {
		qs := []model.DNSQuery{
			q(time.Hour, "10.0.0.5", "log.google.example"),
			q(time.Hour+tc.apart, "10.0.0.5", "log.google.example"),
		}
		d := Analyze(newFakeKB(), week, nil, qs, nil, utcOpt).Devices[0]
		if d.Total != tc.want {
			t.Errorf("%v apart: Total = %d, want %d", tc.apart, d.Total, tc.want)
		}
	}
	// The window runs from the counted lookup, not from the latest query.
	qs := []model.DNSQuery{
		q(time.Hour, "10.0.0.5", "log.google.example"),
		q(time.Hour+50*time.Second, "10.0.0.5", "log.google.example"),
		q(time.Hour+100*time.Second, "10.0.0.5", "log.google.example"),
	}
	if d := Analyze(newFakeKB(), week, nil, qs, nil, utcOpt).Devices[0]; d.Total != 2 {
		t.Errorf("0, 50 s, 100 s: Total = %d, want 2", d.Total)
	}
}

func TestDevicesAndDomainsCountSeparately(t *testing.T) {
	qs := []model.DNSQuery{
		q(time.Hour, "10.0.0.5", "log.google.example"),
		q(time.Hour, "10.0.0.6", "log.google.example"),
		q(time.Hour+time.Second, "10.0.0.5", "track.google.example"),
	}
	r := Analyze(newFakeKB(), week, nil, qs, nil, utcOpt)
	if n := findDevice(t, r, "ip:10.0.0.5").Total; n != 2 {
		t.Errorf("10.0.0.5 Total = %d, want 2", n)
	}
	if n := findDevice(t, r, "ip:10.0.0.6").Total; n != 1 {
		t.Errorf("10.0.0.6 Total = %d, want 1", n)
	}
	if r.Total != 3 {
		t.Errorf("home Total = %d, want 3", r.Total)
	}
}

func TestOutOfOrderInput(t *testing.T) {
	// Two sources merged without sorting: bursts of record types and
	// blocked retries, interleaved across devices and hours.
	rng := rand.New(rand.NewPCG(3, 3))
	var qs []model.DNSQuery
	for i := range 400 {
		at := time.Duration(rng.Int64N(int64(12 * time.Hour)))
		client := []string{"10.0.0.5", "10.0.0.6"}[i%2]
		dom := []string{"ads.samsung.example", "log.google.example", "time.example"}[i%3]
		for k, typ := range []string{"A", "AAAA", "HTTPS"}[:1+i%3] {
			qs = append(qs, qt(at+time.Duration(k)*time.Second, client, dom, typ, i%5 == 0 && k > 0))
		}
	}
	sorted := slices.Clone(qs)
	slices.SortStableFunc(sorted, func(x, y model.DNSQuery) int { return x.Time.Compare(y.Time) })
	shuffled := slices.Clone(qs)
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	keep := slices.Clone(shuffled)

	want := Analyze(newFakeKB(), week, nil, sorted, nil, utcOpt)
	if want.Total >= len(qs) {
		t.Fatalf("Total = %d of %d queries: nothing was merged", want.Total, len(qs))
	}
	for name, in := range map[string][]model.DNSQuery{"merged": qs, "shuffled": shuffled} {
		got := Analyze(newFakeKB(), week, nil, in, nil, utcOpt)
		if got.Total != want.Total || got.Snooping != want.Snooping || len(got.Devices) != len(want.Devices) {
			t.Fatalf("%s: Total/Snooping = %d/%d, want %d/%d", name, got.Total, got.Snooping, want.Total, want.Snooping)
		}
		for i, d := range got.Devices {
			w := want.Devices[i]
			if d.Total != w.Total || d.Blocked != w.Blocked || d.Hourly != w.Hourly || d.QuietHours != w.QuietHours ||
				!slices.EqualFunc(d.Domains, w.Domains, sameDomainStat) {
				t.Errorf("%s: %s = %d/%d %v, want %d/%d %v", name, d.Device.ID,
					d.Total, d.Blocked, d.Domains, w.Total, w.Blocked, w.Domains)
			}
		}
	}
	if !slices.Equal(shuffled, keep) {
		t.Error("Analyze reordered its input")
	}
}

func sameDomainStat(x, y model.DomainStat) bool {
	return x.Domain == y.Domain && x.Count == y.Count && x.Blocked == y.Blocked &&
		x.First.Equal(y.First) && x.Last.Equal(y.Last)
}

func TestHeartbeatsUnchangedByCounting(t *testing.T) {
	// Clocks with ±10% jitter for a day, each beat an A + AAAA pair. The
	// clock is reported exactly as the queries alone show it, including one
	// faster than the window and one at it.
	for _, every := range []time.Duration{15 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute} {
		rng := rand.New(rand.NewPCG(5, uint64(every)))
		var qs []model.DNSQuery
		var raw []int64
		beats := 0
		for at := time.Duration(0); at < 24*time.Hour; beats++ {
			qs = append(qs,
				qt(at, "10.0.0.5", "acr.samsung.example", "A", false),
				qt(at+30*time.Millisecond, "10.0.0.5", "acr.samsung.example", "AAAA", false))
			raw = append(raw, t0.Add(at).UnixNano(), t0.Add(at+30*time.Millisecond).UnixNano())
			at += time.Duration(float64(every) * (0.9 + 0.2*rng.Float64()))
		}
		wantEvery, wantJitter, ok := detectHeartbeat(raw, DefaultMinHeartbeat)
		if !ok || wantEvery != every {
			t.Fatalf("every %v: the queries alone detect %v, %v", every, wantEvery, ok)
		}
		devs := []model.Device{{ID: "mac:tv", IPs: []netip.Addr{ip("10.0.0.5")}}}
		d := Analyze(newFakeKB(), week, devs, qs, nil, utcOpt).Devices[0]
		if len(d.Heartbeats) != 1 || d.Heartbeats[0].Every != wantEvery || d.Heartbeats[0].Jitter != wantJitter {
			t.Errorf("every %v: heartbeats = %+v, want every %v jitter %v", every, d.Heartbeats, wantEvery, wantJitter)
		}
		// Clocks slower than the window count every beat, faster ones at
		// most once per window.
		if every > countWindow && d.Total != beats {
			t.Errorf("every %v: Total = %d, want %d beats", every, d.Total, beats)
		}
		if every < countWindow && (d.Total >= beats || d.Total > int(24*time.Hour/countWindow)+1) {
			t.Errorf("every %v: Total = %d for %d beats, want at most one per %v", every, d.Total, beats, countWindow)
		}
		// A clock at the window counts the beats at least a minute after
		// the last counted one: with jitter, between half and all of them.
		if every == countWindow && (d.Total > beats || d.Total < beats/2) {
			t.Errorf("every %v: Total = %d for %d beats, want between half and all", every, d.Total, beats)
		}
		t.Logf("every %v: %d beats, %d lookups counted", every, beats, d.Total)
	}
}
