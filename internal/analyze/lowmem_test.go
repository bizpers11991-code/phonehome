package analyze

import (
	"fmt"
	"iter"
	"math/rand/v2"
	"net/netip"
	"reflect"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// manyDomains is a household of devices that each look up many distinct
// names, as one does with CDN hash hosts or generated tracker names: every
// (device, domain) pair is looked up a few times. It is the shape that makes
// a report's memory grow with the number of distinct domains.
func manyDomains(devices, domains, perPair int, span time.Duration) ([]model.Device, []model.DNSQuery) {
	rng := rand.New(rand.NewPCG(11, 13))
	devs := make([]model.Device, devices)
	for i := range devs {
		devs[i] = model.Device{ID: fmt.Sprintf("mac:%02d", i), IPs: []netip.Addr{netip.AddrFrom4([4]byte{10, 0, 0, byte(i + 1)})}}
	}
	names := make([]string, domains)
	for i := range names {
		names[i] = fmt.Sprintf("h%d.cdn%d.example", i, i%7)
	}
	qs := make([]model.DNSQuery, 0, devices*domains*perPair)
	for d := range devs {
		for _, n := range names {
			for range perPair {
				qs = append(qs, model.DNSQuery{
					Time:     t0.Add(time.Duration(rng.Int64N(int64(span)))),
					ClientIP: devs[d].IPs[0],
					Domain:   n,
					Blocked:  rng.IntN(10) == 0,
				})
			}
		}
	}
	slices.SortStableFunc(qs, func(a, b model.DNSQuery) int { return a.Time.Compare(b.Time) })
	return devs, qs
}

func noDomains(string) bool { return false }

// TestTopDomainsWithoutFullList: leaving the full list out (DomainsFor)
// changes nothing but that list; TopDomains and Unknown are the same.
func TestTopDomainsWithoutFullList(t *testing.T) {
	devs, qs := manyDomains(3, 700, 3, 3*24*time.Hour) // > topDomainsTrimAt and unknownTrimAt per device
	full := Analyze(newFakeKB(), week, devs, qs, nil, utcOpt)
	o := utcOpt
	o.DomainsFor = noDomains
	lean := Analyze(newFakeKB(), week, devs, qs, nil, o)

	for i, d := range full.Devices {
		l := lean.Devices[i]
		if len(d.Domains) != 700 || l.Domains != nil {
			t.Fatalf("%s: Domains = %d full, %v lean", d.Device.ID, len(d.Domains), len(l.Domains))
		}
		if len(l.TopDomains) != maxTopDomains || len(l.Unknown) != maxUnknown {
			t.Fatalf("%s: lean has %d top, %d unknown", d.Device.ID, len(l.TopDomains), len(l.Unknown))
		}
		// The same report once the list is dropped.
		d.Domains, d.TopDomains = nil, nil
		l.TopDomains = nil
		if !reflect.DeepEqual(d, l) {
			t.Errorf("%s differs without the full list:\n%+v\n%+v", d.Device.ID, d, l)
		}
	}
	for i := range full.Devices {
		if got, want := lean.Devices[i].TopDomains, full.Devices[i].TopDomains; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: TopDomains differ:\n%v\n%v", full.Devices[i].Device.ID, got, want)
		}
	}

	// MaxDomainEntries: devices in order get their list until it is spent.
	o.DomainsFor = nil
	o.MaxDomainEntries = 1500 // two of the three devices' 700
	capped := Analyze(newFakeKB(), week, devs, qs, nil, o)
	for i, d := range capped.Devices {
		if want := d.Device.ID != "mac:02"; (len(d.Domains) > 0) != want || d.DomainCount != 700 { // devs order
			t.Errorf("%s: %d of %d domains listed", d.Device.ID, len(d.Domains), d.DomainCount)
		}
		if !reflect.DeepEqual(d.TopDomains, full.Devices[i].TopDomains) {
			t.Errorf("%s: TopDomains differ", d.Device.ID)
		}
	}

	// Only the devices DomainsFor names get the list.
	o.MaxDomainEntries = 0
	o.DomainsFor = func(id string) bool { return id == "mac:01" }
	one := Analyze(newFakeKB(), week, devs, qs, nil, o)
	for _, d := range one.Devices {
		if want := d.Device.ID == "mac:01"; (len(d.Domains) > 0) != want {
			t.Errorf("%s: %d domains listed", d.Device.ID, len(d.Domains))
		}
	}
}

// TestUnknownTopMatchesSort: trimming as domains arrive gives the head of
// the fully sorted list, ties included.
func TestUnknownTopMatchesSort(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	var tp unknownTop
	var all []model.UnknownDomain
	for i := range 5000 {
		da := &domainAcc{count: int32(1 + rng.IntN(12)), first: int64(i), last: int64(i + 1)} // many ties
		name := fmt.Sprintf("u%d.sub.example.co.uk", rng.IntN(1<<30))
		tp.add(name, da)
		all = append(all, model.UnknownDomain{Domain: name, Count: int(da.count), First: time.Unix(0, da.first), Last: time.Unix(0, da.last)})
	}
	slices.SortFunc(all, func(x, y model.UnknownDomain) int {
		if x.Count != y.Count {
			return y.Count - x.Count
		}
		return cmpString(x.Domain, y.Domain)
	})
	want := all[:maxUnknown]
	got := tp.result()
	if len(got) != maxUnknown {
		t.Fatalf("got %d", len(got))
	}
	for i := range got {
		w := want[i]
		w.Group = Registrable(w.Domain)
		if got[i] != w {
			t.Fatalf("[%d] = %+v, want %+v", i, got[i], w)
		}
	}
	if (&unknownTop{}).result() != nil {
		t.Error("no unknown domains: result is not nil")
	}
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// TestAnalyzeStreamMatchesSlice: a sequence gives the same report as the
// slice, with and without the comparison, and the producer may reuse the
// lookup it yields.
func TestAnalyzeStreamMatchesSlice(t *testing.T) {
	p := model.Period{From: t0.Add(7 * 24 * time.Hour), To: t0.Add(14 * 24 * time.Hour)}
	devs, qs := manyDomains(2, 50, 4, 14*24*time.Hour)
	stream := func() iter.Seq[*model.DNSQuery] {
		return func(yield func(*model.DNSQuery) bool) {
			var reused model.DNSQuery
			for _, x := range qs {
				reused = x
				if !yield(&reused) {
					return
				}
			}
		}
	}
	for name, from := range map[string]time.Time{"compared": t0, "alone": {}} {
		want := AnalyzeCompared(newFakeKB(), p, devs, qs, nil, utcOpt, from)
		got := AnalyzeStream(newFakeKB(), p, devs, stream(), nil, utcOpt, from)
		want.GeneratedAt, got.GeneratedAt = time.Time{}, time.Time{}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: stream and slice reports differ", name)
		}
		if name == "compared" && want.Previous == nil {
			t.Error("no comparison made")
		}
	}
}

// TestPreviousPeriodHasNoDomainList: the previous period is only compared
// with, so its devices (HomeComparison.Gone) carry no domain lists.
func TestPreviousPeriodHasNoDomainList(t *testing.T) {
	p := model.Period{From: t0.Add(7 * 24 * time.Hour), To: t0.Add(14 * 24 * time.Hour)}
	devs := []model.Device{
		{ID: "mac:now", IPs: []netip.Addr{ip("10.0.0.1")}},
		{ID: "mac:gone", IPs: []netip.Addr{ip("10.0.0.2")}},
	}
	qs := []model.DNSQuery{
		q(24*time.Hour, "10.0.0.1", "time.example"), q(25*time.Hour, "10.0.0.2", "time.example"),
		q(8*24*time.Hour, "10.0.0.1", "time.example"),
	}
	r := AnalyzeCompared(newFakeKB(), p, devs, qs, nil, utcOpt, t0)
	if r.Previous == nil || len(r.Previous.Gone) != 1 {
		t.Fatalf("Previous = %+v", r.Previous)
	}
	if g := r.Previous.Gone[0]; g.Total != 1 || g.Domains != nil || g.TopDomains != nil {
		t.Errorf("gone device: total %d, %d domains, %d top", g.Total, len(g.Domains), len(g.TopDomains))
	}
	if d := findDevice(t, r, "mac:now"); len(d.Domains) != 1 || len(d.TopDomains) != 1 {
		t.Errorf("current device: %d domains, %d top", len(d.Domains), len(d.TopDomains))
	}
}

// TestManyDomainsMemory fails if a report's memory per (device, domain) pair
// grows back toward what it was: ~740 bytes at peak before accumulators were
// packed and per-domain classifications shared. It measures the live heap
// while the analysis holds its accumulators, so it is a guard rather than a
// benchmark; the bound has plenty of room for the runtime's own noise.
func TestManyDomainsMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates about 100 MB")
	}
	const devices, domains, perPair = 8, 15000, 3 // 120k pairs, 360k lookups
	devs, qs := manyDomains(devices, domains, perPair, 5*24*time.Hour)
	o := utcOpt
	o.DomainsFor = noDomains

	var peak uint64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var ms runtime.MemStats
		for {
			runtime.ReadMemStats(&ms)
			peak = max(peak, ms.HeapAlloc)
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
		}
	}()
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	r := Analyze(newFakeKB(), week, devs, qs, nil, o)
	var end runtime.MemStats
	runtime.ReadMemStats(&end)
	close(stop)
	<-done

	pairs := devices * domains
	peakPerPair := float64(peak-base.HeapAlloc) / float64(pairs)
	t.Logf("%d pairs: heap peak +%.1f MiB (%.0f B/pair), allocated %.1f MiB", pairs,
		float64(peak-base.HeapAlloc)/(1<<20), peakPerPair, float64(end.TotalAlloc-base.TotalAlloc)/(1<<20))
	if peakPerPair > 450 {
		t.Errorf("heap peak is %.0f bytes per (device, domain) pair, want under 450", peakPerPair)
	}
	if r.Total == 0 || r.Devices[0].Domains != nil {
		t.Errorf("unexpected report: %d lookups", r.Total)
	}
	runtime.KeepAlive(qs)
}

// BenchmarkAnalyzeManyDomains is a household whose devices each look up
// 20,000 distinct names (160k pairs, 480k lookups in a week); watch B/op.
func BenchmarkAnalyzeManyDomains(b *testing.B) {
	devs, qs := manyDomains(8, 20000, 3, 7*24*time.Hour)
	o := utcOpt
	o.DomainsFor = noDomains
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		r := Analyze(benchClassifier, week, devs, qs, nil, o)
		if len(r.Devices) != 8 {
			b.Fatal(len(r.Devices))
		}
	}
}
