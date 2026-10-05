package analyze

import (
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// fakeKB classifies by exact domain and records how it was called.
type fakeKB struct {
	rules    map[string]model.Classification
	classify map[string]int // calls per domain
	fixesFor []string       // "<device id> <kind> <company ids>" per call
}

func newFakeKB() *fakeKB {
	samsung := &model.Company{ID: "samsung", Name: "Samsung", Country: "KR"}
	google := &model.Company{ID: "google", Name: "Google", Country: "US"}
	netflix := &model.Company{ID: "netflix", Name: "Netflix", Country: "US"}
	rules := map[string]model.Classification{
		"acr.samsung.example":   {Category: model.CatACR, Company: samsung, KindHint: model.KindTV, Purpose: "Fingerprints the screen."},
		"ads.samsung.example":   {Category: model.CatAds, Company: samsung, KindHint: model.KindTV},
		"log.google.example":    {Category: model.CatTelemetry, Company: google},
		"track.google.example":  {Category: model.CatTracking, Company: google},
		"time.example":          {Category: model.CatEssential},
		"video.netflix.example": {Category: model.CatContent, Company: netflix},
		"weird.example":         {Category: "bogus"},
	}
	for d, c := range rules {
		c.Domain = d
		rules[d] = c
	}
	return &fakeKB{rules: rules, classify: map[string]int{}}
}

func (k *fakeKB) Classify(domain string) model.Classification {
	k.classify[domain]++
	if c, ok := k.rules[domain]; ok {
		return c
	}
	return model.Classification{Domain: domain, Category: model.CatUnknown}
}

func (k *fakeKB) FixesFor(d model.Device, ids []string) []model.Fix {
	k.fixesFor = append(k.fixesFor, fmt.Sprintf("%s %s %s", d.ID, d.Kind, strings.Join(ids, ",")))
	return []model.Fix{{ID: "fix-" + d.ID}}
}

var (
	t0     = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	week   = model.Period{From: t0, To: t0.Add(7 * 24 * time.Hour)}
	utcOpt = Options{Location: time.UTC}
)

func ip(s string) netip.Addr { return netip.MustParseAddr(s) }

func q(at time.Duration, client, domain string) model.DNSQuery {
	return model.DNSQuery{Time: t0.Add(at), ClientIP: ip(client), Domain: domain}
}

func findDevice(t *testing.T, r model.HomeReport, id string) model.DeviceReport {
	t.Helper()
	for _, d := range r.Devices {
		if d.Device.ID == id {
			return d
		}
	}
	t.Fatalf("device %q not in report; have %v", id, deviceIDs(r))
	return model.DeviceReport{}
}

func deviceIDs(r model.HomeReport) []string {
	var ids []string
	for _, d := range r.Devices {
		ids = append(ids, d.Device.ID)
	}
	return ids
}

func TestOptionsNormalized(t *testing.T) {
	tests := []struct {
		name       string
		in         Options
		start, end int
		minHB      int
	}{
		{"zero value", Options{}, 1, 6, 20},
		{"explicit", Options{QuietStart: 23, QuietEnd: 6, MinHeartbeat: 5}, 23, 6, 5},
		{"midnight start", Options{QuietStart: 0, QuietEnd: 5}, 0, 5, 20},
		{"ends at midnight", Options{QuietStart: 22, QuietEnd: 0}, 22, 0, 20},
		{"empty window", Options{QuietStart: 4, QuietEnd: 4}, 1, 6, 20},
		{"out of range", Options{QuietStart: 22, QuietEnd: 24}, 1, 6, 20},
		{"negative heartbeat", Options{MinHeartbeat: -1}, 1, 6, 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in.normalized()
			if got.QuietStart != tt.start || got.QuietEnd != tt.end || got.MinHeartbeat != tt.minHB {
				t.Errorf("got %d–%d min %d, want %d–%d min %d",
					got.QuietStart, got.QuietEnd, got.MinHeartbeat, tt.start, tt.end, tt.minHB)
			}
			if got.Location == nil {
				t.Error("Location is nil")
			}
		})
	}
	if d := DefaultOptions(); !reflect.DeepEqual(d.normalized(), d) {
		t.Errorf("DefaultOptions not a fixed point of normalized: %+v", d)
	}
}

func TestQuietHours(t *testing.T) {
	tests := []struct {
		start, end int
		in, out    []int
		label      string
	}{
		{1, 6, []int{1, 3, 5}, []int{0, 6, 23}, "01:00–06:00"},
		{23, 6, []int{23, 0, 5}, []int{6, 12, 22}, "23:00–06:00"},
		{22, 0, []int{22, 23}, []int{0, 21}, "22:00–00:00"},
		{0, 1, []int{0}, []int{1, 23}, "00:00–01:00"},
	}
	for _, tt := range tests {
		o := Options{QuietStart: tt.start, QuietEnd: tt.end}.normalized()
		for _, h := range tt.in {
			if !o.quiet(h) {
				t.Errorf("%s: hour %d should be quiet", tt.label, h)
			}
		}
		for _, h := range tt.out {
			if o.quiet(h) {
				t.Errorf("%s: hour %d should not be quiet", tt.label, h)
			}
		}
		if got := o.quietLabel(); got != tt.label {
			t.Errorf("label = %q, want %q", got, tt.label)
		}
	}
}

func TestQuietHoursCountedInLocation(t *testing.T) {
	// 23:30 UTC is 01:30 in Berlin (CEST, UTC+2) on this date.
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	qs := []model.DNSQuery{
		q(23*time.Hour+30*time.Minute, "10.0.0.9", "time.example"),
		q(12*time.Hour, "10.0.0.9", "time.example"),
	}
	r := Analyze(newFakeKB(), week, nil, qs, nil, Options{Location: berlin})
	d := r.Devices[0]
	if d.QuietHours != 1 {
		t.Errorf("QuietHours = %d, want 1", d.QuietHours)
	}
	if d.Hourly[1] != 1 || d.Hourly[14] != 1 {
		t.Errorf("Hourly = %v, want hits at 01 and 14", d.Hourly)
	}
}

func TestAttribution(t *testing.T) {
	devs := []model.Device{
		{ID: "mac:old", IPs: []netip.Addr{ip("10.0.0.5")}, Hostname: "old-laptop", LastSeen: t0},
		{ID: "mac:new", IPs: []netip.Addr{ip("10.0.0.5"), ip("fe80::1")}, Hostname: "samsung-tv", LastSeen: t0.Add(time.Hour)},
		{ID: "mac:idle", IPs: []netip.Addr{ip("10.0.0.7")}, Hostname: "idle"},
		{ID: "mac:flowonly", IPs: []netip.Addr{ip("10.0.0.8")}, Hostname: "fridge"},
	}
	qs := []model.DNSQuery{
		q(0, "10.0.0.5", "time.example"),                  // at From: counts
		q(7*24*time.Hour, "10.0.0.5", "time.example"),     // at To: excluded
		q(-time.Second, "10.0.0.5", "time.example"),       // before: excluded
		q(time.Hour, "::ffff:10.0.0.5", "time.example"),   // v4-mapped → same device
		q(2*time.Hour, "fe80::1%eth0", "time.example"),    // zone stripped
		q(3*time.Hour, "10.0.0.99", "log.google.example"), // unknown → synthetic
		q(-time.Hour, "10.0.0.7", "log.google.example"),   // idle only outside period
	}
	fl := []model.Flow{
		{Start: t0.Add(time.Hour), ClientIP: ip("10.0.0.8"), RemoteIP: ip("192.0.2.1"), RemotePort: 443, Proto: "tcp"},
		{Start: t0.Add(-time.Hour), ClientIP: ip("10.0.0.8"), RemoteIP: ip("192.0.2.1"), RemotePort: 443, Proto: "tcp"},
	}
	r := Analyze(newFakeKB(), week, devs, qs, fl, utcOpt)

	if got, want := deviceIDs(r), []string{"ip:10.0.0.99", "mac:flowonly", "mac:new"}; !slices.Equal(got, want) {
		t.Fatalf("devices = %v, want %v", got, want)
	}
	if n := findDevice(t, r, "mac:new").Total; n != 3 {
		t.Errorf("mac:new Total = %d, want 3", n)
	}
	if n := findDevice(t, r, "mac:flowonly").Flows; n != 1 {
		t.Errorf("mac:flowonly Flows = %d, want 1", n)
	}
	syn := findDevice(t, r, "ip:10.0.0.99").Device
	if syn.Kind != model.KindUnknown || !slices.Equal(syn.IPs, []netip.Addr{ip("10.0.0.99")}) {
		t.Errorf("synthetic device = %+v", syn)
	}
	if want := t0.Add(3 * time.Hour); !syn.FirstSeen.Equal(want) || !syn.LastSeen.Equal(want) {
		t.Errorf("synthetic seen = %v..%v, want %v", syn.FirstSeen, syn.LastSeen, want)
	}
	if r.Total != 4 {
		t.Errorf("home Total = %d, want 4", r.Total)
	}
}

func TestSharedIPTieIsDeterministic(t *testing.T) {
	a := model.Device{ID: "mac:b", IPs: []netip.Addr{ip("10.0.0.5")}}
	b := model.Device{ID: "mac:a", IPs: []netip.Addr{ip("10.0.0.5")}}
	qs := []model.DNSQuery{q(0, "10.0.0.5", "time.example")}
	for _, devs := range [][]model.Device{{a, b}, {b, a}} {
		r := Analyze(newFakeKB(), week, devs, qs, nil, utcOpt)
		if got := deviceIDs(r); !slices.Equal(got, []string{"mac:a"}) {
			t.Errorf("devices = %v, want [mac:a]", got)
		}
	}
}

func TestAggregates(t *testing.T) {
	kb := newFakeKB()
	var qs []model.DNSQuery
	// Repeats are a minute apart, so each is a lookup of its own.
	add := func(n int, domain string, at time.Duration, blocked bool) {
		for i := range n {
			x := q(at+time.Duration(i)*time.Minute, "10.0.0.5", domain)
			x.Blocked = blocked
			qs = append(qs, x)
		}
	}
	add(10, "acr.samsung.example", 2*time.Hour, false) // quiet hour 02
	add(4, "ads.samsung.example", 10*time.Hour, true)
	add(3, "log.google.example", 10*time.Hour, false)
	add(2, "time.example", 10*time.Hour, false)
	add(1, "video.netflix.example", 10*time.Hour, false)
	add(1, "weird.example", 10*time.Hour, false)
	for i := range 30 { // 30 unknown domains, 1 lookup each, to exercise the cap
		add(1, fmt.Sprintf("u%02d.example", i), 11*time.Hour, false)
	}
	devs := []model.Device{{ID: "mac:tv", IPs: []netip.Addr{ip("10.0.0.5")}, Hostname: "living-room"}}
	r := Analyze(kb, week, devs, qs, nil, utcOpt)
	d := findDevice(t, r, "mac:tv")

	wantCat := map[model.Category]int{
		model.CatACR: 10, model.CatAds: 4, model.CatTracking: 0, model.CatTelemetry: 3,
		model.CatEssential: 2, model.CatContent: 1, model.CatUnknown: 31,
	}
	for _, c := range model.Categories() {
		if n, ok := d.ByCategory[c]; !ok || n != wantCat[c] {
			t.Errorf("ByCategory[%s] = %d (present %v), want %d", c, n, ok, wantCat[c])
		}
		if r.ByCategory[c] != wantCat[c] {
			t.Errorf("home ByCategory[%s] = %d, want %d", c, r.ByCategory[c], wantCat[c])
		}
	}
	if d.Total != 51 || d.Snooping != 17 || d.Blocked != 4 || r.Snooping != 17 {
		t.Errorf("Total/Snooping/Blocked = %d/%d/%d, want 51/17/4", d.Total, d.Snooping, d.Blocked)
	}
	if want := 17.0 / 51; d.SnoopShare != want {
		t.Errorf("SnoopShare = %v, want %v", d.SnoopShare, want)
	}
	// Rates are per day of data: the period runs from the first lookup.
	first := r.Period.To
	for _, q := range qs {
		if q.Time.Before(first) && !q.Time.Before(week.From) {
			first = q.Time
		}
	}
	if want := 17.0 / (model.Period{From: first, To: week.To}).Days(); d.PerDay != want {
		t.Errorf("PerDay = %v, want %v", d.PerDay, want)
	}
	if d.Hourly[2] != 10 || d.Hourly[10] != 11 || d.Hourly[11] != 30 {
		t.Errorf("Hourly = %v", d.Hourly)
	}
	if d.QuietHours != 10 || d.QuietLabel != "01:00–06:00" {
		t.Errorf("QuietHours = %d %q", d.QuietHours, d.QuietLabel)
	}

	if len(d.TopDomains) != 25 {
		t.Fatalf("len(TopDomains) = %d, want 25", len(d.TopDomains))
	}
	top := d.TopDomains[0]
	if top.Domain != "acr.samsung.example" || top.Count != 10 || top.Category != model.CatACR ||
		top.CompanyID != "samsung" || top.CompanyName != "Samsung" || top.Purpose == "" {
		t.Errorf("TopDomains[0] = %+v", top)
	}
	if b := d.TopDomains[1]; b.Domain != "ads.samsung.example" || b.Blocked != 4 {
		t.Errorf("TopDomains[1] = %+v", b)
	}
	// Domains holds every domain, TopDomains its start.
	if len(d.Domains) <= len(d.TopDomains) || d.Domains[0].Domain != top.Domain || d.Domains[24].Domain != d.TopDomains[24].Domain {
		t.Errorf("Domains: %d entries, starting %v", len(d.Domains), d.Domains[0])
	}
	if top.First.IsZero() || top.Last.Before(top.First) || top.First.Before(week.From) {
		t.Errorf("TopDomains[0] seen %v – %v", top.First, top.Last)
	}
	// Ties at count 1 are ordered by name: u00 sorts before video.netflix.
	if got := d.TopDomains[4].Domain; got != "u00.example" {
		t.Errorf("TopDomains[4] = %q, want u00.example", got)
	}

	wantCo := []model.CompanyStat{
		{ID: "samsung", Name: "Samsung", Country: "KR", Count: 14},
		{ID: "google", Name: "Google", Country: "US", Count: 3},
		{ID: "netflix", Name: "Netflix", Country: "US", Count: 1},
	}
	if !slices.Equal(d.Companies, wantCo) {
		t.Errorf("Companies = %+v, want %+v", d.Companies, wantCo)
	}
	if !slices.Equal(d.Countries, []string{"KR", "US"}) {
		t.Errorf("Countries = %v", d.Countries)
	}

	// Strong ACR evidence makes it a TV, which is what FixesFor receives.
	if d.Device.Kind != model.KindTV {
		t.Errorf("Kind = %s, want tv", d.Device.Kind)
	}
	if want := []string{"mac:tv tv samsung,google,netflix"}; !slices.Equal(kb.fixesFor, want) {
		t.Errorf("FixesFor calls = %v, want %v", kb.fixesFor, want)
	}
	if len(d.Fixes) != 1 || d.Fixes[0].ID != "fix-mac:tv" {
		t.Errorf("Fixes = %+v", d.Fixes)
	}
	if d.Grade != "D" { // ACR lookups, no ACR heartbeat
		t.Errorf("Grade = %s, want D", d.Grade)
	}
	if r.Grade != "D" { // the only device's grade
		t.Errorf("home Grade = %s, want D", r.Grade)
	}
	for dom, n := range kb.classify {
		if n != 1 {
			t.Errorf("Classify(%q) called %d times, want 1", dom, n)
		}
	}
}

func TestDeviceOrder(t *testing.T) {
	devs := []model.Device{
		{ID: "mac:1", IPs: []netip.Addr{ip("10.0.0.1")}, Hostname: "zeta"},
		{ID: "mac:2", IPs: []netip.Addr{ip("10.0.0.2")}, Hostname: "alpha"},
		{ID: "mac:3", IPs: []netip.Addr{ip("10.0.0.3")}, Hostname: "beta"},
	}
	qs := []model.DNSQuery{
		q(0, "10.0.0.1", "log.google.example"),
		q(time.Minute, "10.0.0.1", "log.google.example"),
		q(0, "10.0.0.2", "log.google.example"),
		q(0, "10.0.0.3", "log.google.example"),
	}
	r := Analyze(newFakeKB(), week, devs, qs, nil, utcOpt)
	if got, want := deviceIDs(r), []string{"mac:1", "mac:2", "mac:3"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestAnalyzeEmpty(t *testing.T) {
	fixed := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	defer func(f func() time.Time) { now = f }(now)
	now = func() time.Time { return fixed }

	r := Analyze(newFakeKB(), week, []model.Device{{ID: "mac:x"}}, nil, nil, Options{})
	if len(r.Devices) != 0 || r.Total != 0 || r.Snooping != 0 || r.Grade != "" {
		t.Errorf("report = %+v, want empty", r)
	}
	if len(r.ByCategory) != len(model.Categories()) {
		t.Errorf("ByCategory = %v, want every category", r.ByCategory)
	}
	if !r.GeneratedAt.Equal(fixed) || r.Period != week {
		t.Errorf("GeneratedAt/Period = %v/%v", r.GeneratedAt, r.Period)
	}
}

func TestRelevantFixes(t *testing.T) {
	brandACR := model.Fix{ID: "brand-acr", Vendors: []string{"acme"}, When: []string{"acr"}}
	brandAds := model.Fix{ID: "brand-ads", Vendors: []string{"acme"}}
	genericACR := model.Fix{ID: "generic-acr", When: []string{"acr"}}
	genericBypass := model.Fix{ID: "generic-bypass", When: []string{"bypass"}}
	all := []model.Fix{brandAds, genericACR, brandACR, genericBypass}

	ids := func(fs []model.Fix) []string {
		var out []string
		for _, f := range fs {
			out = append(out, f.ID)
		}
		return out
	}
	withACR := model.DeviceReport{ByCategory: map[model.Category]int{model.CatACR: 3}}
	if got, want := ids(relevantFixes(all, withACR)), []string{"brand-acr", "brand-ads", "generic-acr"}; !slices.Equal(got, want) {
		t.Errorf("with ACR: got %v, want %v", got, want)
	}
	quiet := model.DeviceReport{ByCategory: map[model.Category]int{}, Bypasses: []model.Bypass{{Kind: BypassDoTFlow}}}
	if got, want := ids(relevantFixes(all, quiet)), []string{"brand-ads", "generic-bypass"}; !slices.Equal(got, want) {
		t.Errorf("without ACR: got %v, want %v", got, want)
	}
}

// A fresh install viewing a long period must be rated on the days it has data,
// not diluted by the empty days before it started.
func TestPerDayUsesCoveredPeriod(t *testing.T) {
	end := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	tv := netip.MustParseAddr("192.168.1.20")
	var qs []model.DNSQuery
	for i := range 200 { // 200 lookups over the last 2 days
		qs = append(qs, model.DNSQuery{Time: end.Add(-48*time.Hour + time.Duration(i)*14*time.Minute), ClientIP: tv, Domain: "ads.samsung.example"})
	}
	c := newFakeKB()
	for _, days := range []int{2, 30} {
		p := model.Period{From: end.Add(-time.Duration(days) * 24 * time.Hour), To: end}
		r := Analyze(c, p, nil, qs, nil, Options{})
		if got := r.Devices[0].PerDay; got < 99 || got > 101 {
			t.Errorf("%d-day period: PerDay = %.1f, want ~100", days, got)
		}
	}
}
