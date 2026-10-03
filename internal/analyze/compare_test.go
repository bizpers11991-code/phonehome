package analyze

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

const day = 24 * time.Hour

// Two weeks: prevWeek is the comparison baseline for thisWeek.
var (
	thisWeek = model.Period{From: t0.Add(7 * day), To: t0.Add(14 * day)}
	prevWeek = model.Period{From: t0, To: t0.Add(7 * day)}
)

// beat makes n lookups of domain from client every interval, starting at from.
func beat(from time.Time, n int, every time.Duration, client, domain string) []model.DNSQuery {
	qs := make([]model.DNSQuery, n)
	for i := range qs {
		qs[i] = model.DNSQuery{Time: from.Add(time.Duration(i) * every), ClientIP: ip(client), Domain: domain}
	}
	return qs
}

// spread makes n lookups of domain from client over p, starting at p.From,
// irregularly enough not to be a heartbeat.
func spread(p model.Period, n int, client, domain string) []model.DNSQuery {
	step := p.To.Sub(p.From) / time.Duration(n)
	qs := beat(p.From, n, step, client, domain)
	for i := range qs {
		if i%2 == 1 {
			qs[i].Time = qs[i].Time.Add(step * 8 / 10)
		}
	}
	return qs
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestPeriodPrevious(t *testing.T) {
	if got := thisWeek.Previous(); got != prevWeek {
		t.Errorf("Previous() = %v, want %v", got, prevWeek)
	}
}

func TestComparisonFixed(t *testing.T) {
	const tv, bulb, lamp = "10.0.0.20", "10.0.0.30", "10.0.0.40"
	var qs []model.DNSQuery
	// Before: the TV fingerprints the screen every minute for a day (an ACR
	// heartbeat → F) and makes 700 ad lookups. After: 70 ad lookups only.
	qs = append(qs, beat(prevWeek.From.Add(time.Hour), 24*60, time.Minute, tv, "acr.samsung.example")...)
	qs = append(qs, spread(prevWeek, 700, tv, "ads.samsung.example")...)
	qs = append(qs, spread(thisWeek, 70, tv, "ads.samsung.example")...)
	// The bulb is the same both weeks (A → A); the lamp was only around before.
	qs = append(qs, spread(prevWeek, 14, bulb, "log.google.example")...)
	qs = append(qs, spread(thisWeek, 14, bulb, "log.google.example")...)
	qs = append(qs, spread(prevWeek, 50, lamp, "time.example")...)
	// A phone that is new this week.
	qs = append(qs, spread(thisWeek, 7, "10.0.0.50", "track.google.example")...)
	sortByTime(qs)

	k := newFakeKB()
	r := AnalyzeCompared(k, thisWeek, nil, qs, nil, utcOpt, t0)
	pc := r.Previous
	if pc == nil {
		t.Fatal("no comparison with a fully covered previous week")
	}
	if pc.Period != prevWeek || pc.Partial || !near(pc.Days, 7) || !pc.Seen {
		t.Errorf("home comparison period %v partial=%v days=%v seen=%v", pc.Period, pc.Partial, pc.Days, pc.Seen)
	}
	if pc.Grade != "F" || r.Grade != "A" {
		t.Errorf("home grade %s → %s, want F → A", pc.Grade, r.Grade)
	}
	wantPrev := float64(24*60+700+14) / 7
	if !near(pc.PerDay, wantPrev) || !near(pc.NowPerDay, float64(70+14+7)/7) {
		t.Errorf("home per day %.2f → %.2f, want %.2f → %.2f", pc.PerDay, pc.NowPerDay, wantPrev, float64(91)/7)
	}
	if pc.Devices != 3 {
		t.Errorf("previous devices = %d, want 3", pc.Devices)
	}
	if len(pc.Gone) != 1 || pc.Gone[0].Device.ID != "ip:"+lamp {
		t.Errorf("gone = %v, want the lamp", pc.Gone)
	}
	if got := pc.CategoryDelta[model.CatACR]; !near(got, -24*60.0/7) {
		t.Errorf("home ACR delta = %v, want %v", got, -24*60.0/7)
	}
	if k.classify["ads.samsung.example"] != 1 {
		t.Errorf("classified %d times across both periods, want once", k.classify["ads.samsung.example"])
	}
	for _, f := range k.fixesFor {
		if strings.HasPrefix(f, "ip:"+lamp) {
			t.Errorf("fixes looked up for the previous period: %q", f)
		}
	}

	d := findDevice(t, r, "ip:"+tv).Previous
	if d == nil || !d.Seen || d.Grade != "F" || findDevice(t, r, "ip:"+tv).Grade != "A" {
		t.Fatalf("TV comparison %+v", d)
	}
	if ch, ok := d.Change(); !ok || !near(ch, (10.0-(24*60+700)/7.0)/((24*60+700)/7.0)) {
		t.Errorf("TV change = %v, %v", ch, ok)
	}
	if len(d.Stopped) != 1 || d.Stopped[0].Domain != "acr.samsung.example" || len(d.Started) != 0 {
		t.Errorf("TV stopped %v started %v, want the ACR heartbeat stopped", d.Stopped, d.Started)
	}
	if d.ByCategory[model.CatACR] != 24*60 || !near(d.CategoryDelta[model.CatAds], (70-700)/7.0) {
		t.Errorf("TV categories before %v, delta %v", d.ByCategory, d.CategoryDelta)
	}

	b := findDevice(t, r, "ip:"+bulb).Previous
	if ch, ok := b.Change(); !ok || ch != 0 || b.Grade != "A" || findDevice(t, r, "ip:"+bulb).Grade != "A" {
		t.Errorf("bulb: change %v %v, grade %s, want unchanged A", ch, ok, b.Grade)
	}

	n := findDevice(t, r, "ip:10.0.0.50").Previous
	if n == nil || n.Seen || n.Grade != "" || n.Total != 0 || n.Started != nil {
		t.Errorf("new device comparison %+v, want Seen=false", n)
	}
	if _, ok := n.Change(); ok {
		t.Error("new device has a percentage change")
	}
	if !near(n.CategoryDelta[model.CatTracking], 1) {
		t.Errorf("new device tracking delta %v, want +1/day", n.CategoryDelta[model.CatTracking])
	}
}

func TestComparisonPartial(t *testing.T) {
	const tv = "10.0.0.20"
	dataFrom := prevWeek.From.Add(3 * day) // 4 of the previous 7 days have data
	qs := spread(model.Period{From: dataFrom, To: prevWeek.To}, 400, tv, "ads.samsung.example")
	qs = append(qs, spread(thisWeek, 700, tv, "ads.samsung.example")...)
	r := AnalyzeCompared(newFakeKB(), thisWeek, nil, qs, nil, utcOpt, dataFrom)
	pc := r.Previous
	if pc == nil {
		t.Fatal("no comparison with 4 of 7 days covered")
	}
	if !pc.Partial || !near(pc.Days, 4) {
		t.Errorf("partial=%v days=%v, want true, 4", pc.Partial, pc.Days)
	}
	d := r.Devices[0].Previous
	if !near(d.PerDay, 100) || !near(d.NowPerDay, 100) {
		t.Errorf("per day %v → %v, want 100 → 100 (rated per day of data)", d.PerDay, d.NowPerDay)
	}

	// Data that starts a minute late still covers the period.
	r = AnalyzeCompared(newFakeKB(), thisWeek, nil, qs, nil, utcOpt, prevWeek.From.Add(30*time.Second))
	if r.Previous == nil || r.Previous.Partial {
		t.Errorf("30 s late start: %+v, want a full comparison", r.Previous)
	}
}

func TestNoComparison(t *testing.T) {
	const tv = "10.0.0.20"
	cur := spread(thisWeek, 70, tv, "ads.samsung.example")
	prevAndCur := append(spread(prevWeek, 70, tv, "ads.samsung.example"), cur...)
	cases := []struct {
		name     string
		qs       []model.DNSQuery
		dataFrom time.Time
	}{
		{"data begins unknown", prevAndCur, time.Time{}},
		{"data begins in this period", cur, thisWeek.From.Add(time.Hour)},
		{"data begins exactly at this period", cur, thisWeek.From},
		{"less than half the previous period", prevAndCur, prevWeek.From.Add(4 * day)},
		{"previous period covered but empty", cur, t0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := AnalyzeCompared(newFakeKB(), thisWeek, nil, c.qs, nil, utcOpt, c.dataFrom)
			if r.Previous != nil {
				t.Errorf("Previous = %+v, want nil", r.Previous)
			}
			for _, d := range r.Devices {
				if d.Previous != nil {
					t.Errorf("%s: Previous = %+v, want nil", d.Device.ID, d.Previous)
				}
			}
			// Without a comparison the report is exactly Analyze's.
			want := Analyze(newFakeKB(), thisWeek, nil, c.qs, nil, utcOpt)
			if r.Total != want.Total || r.Grade != want.Grade || len(r.Devices) != len(want.Devices) {
				t.Errorf("report differs from Analyze: %d/%s vs %d/%s", r.Total, r.Grade, want.Total, want.Grade)
			}
		})
	}
}

// The current period's figures must not change because a comparison was made.
func TestComparisonLeavesReportAlone(t *testing.T) {
	const tv = "10.0.0.20"
	qs := append(beat(prevWeek.From, 24*60, time.Minute, tv, "acr.samsung.example"),
		beat(thisWeek.From, 24*60, time.Minute, tv, "acr.samsung.example")...)
	qs = append(qs, spread(thisWeek, 100, tv, "video.netflix.example")...)
	sortByTime(qs)
	plain := Analyze(newFakeKB(), thisWeek, nil, qs, nil, utcOpt)
	cmpd := AnalyzeCompared(newFakeKB(), thisWeek, nil, qs, nil, utcOpt, t0)
	a, b := plain.Devices[0], cmpd.Devices[0]
	if a.Total != b.Total || a.PerDay != b.PerDay || a.Grade != b.Grade || len(a.Heartbeats) != len(b.Heartbeats) || len(a.Fixes) != len(b.Fixes) {
		t.Errorf("report changed: %+v vs %+v", a, b)
	}
	if p := b.Previous; p == nil || p.Grade != "F" || len(p.Stopped) != 0 || len(p.Started) != 0 {
		t.Errorf("unchanged heartbeat: %+v", p)
	}
}

func TestChangeDivisionByZero(t *testing.T) {
	cases := []struct {
		c    model.Comparison
		want float64
		ok   bool
	}{
		{model.Comparison{Seen: true, PerDay: 0, NowPerDay: 0}, 0, true},
		{model.Comparison{Seen: true, PerDay: 0, NowPerDay: 5}, 0, false},
		{model.Comparison{Seen: true, PerDay: 100, NowPerDay: 8}, -0.92, true},
		{model.Comparison{Seen: true, PerDay: 10, NowPerDay: 15}, 0.5, true},
		{model.Comparison{Seen: false, NowPerDay: 15}, 0, false},
	}
	for _, c := range cases {
		got, ok := c.c.Change()
		if ok != c.ok || !near(got, c.want) || math.IsNaN(got) || math.IsInf(got, 0) {
			t.Errorf("%+v: Change() = %v, %v; want %v, %v", c.c, got, ok, c.want, c.ok)
		}
	}
}

func sortByTime(qs []model.DNSQuery) {
	slices.SortStableFunc(qs, func(a, b model.DNSQuery) int { return a.Time.Compare(b.Time) })
}
