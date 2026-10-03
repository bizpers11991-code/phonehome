package demo

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/kb"
	"github.com/bizpers11991-code/phonehome/internal/model"
)

var end = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func TestDeterministic(t *testing.T) {
	a := Generate(42, end, 3)
	b := Generate(42, end, 3)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed produced different households")
	}
	c := Generate(43, end, 3)
	if reflect.DeepEqual(a.DNS, c.DNS) {
		t.Fatal("different seeds produced identical lookups")
	}
}

func TestVolumesAndBounds(t *testing.T) {
	start := time.Now()
	h := Generate(1, end, 7)
	if d := time.Since(start); d > 2*time.Second { // generous for -race
		t.Errorf("Generate took %v", d)
	}
	if n := len(h.DNS); n < 100_000 || n > 200_000 {
		t.Errorf("7 days = %d lookups, want 100k–200k", n)
	}
	from := end.Add(-7 * 24 * time.Hour)
	if !slices.IsSortedFunc(h.DNS, func(a, b model.DNSQuery) int { return a.Time.Compare(b.Time) }) {
		t.Error("DNS not sorted by time")
	}
	if h.DNS[0].Time.Before(from) || !h.DNS[len(h.DNS)-1].Time.Before(end) {
		t.Errorf("lookups outside [%v, %v): %v … %v", from, end, h.DNS[0].Time, h.DNS[len(h.DNS)-1].Time)
	}

	perDay := map[string]float64{}
	ip2name := map[string]string{}
	for _, d := range h.Devices {
		ip2name[d.IPs[0].String()] = d.Label
	}
	blocked := 0
	for _, q := range h.DNS {
		name, ok := ip2name[q.ClientIP.String()]
		if !ok {
			t.Fatalf("lookup from unknown client %v", q.ClientIP)
		}
		perDay[name] += 1.0 / 7
		if q.Blocked {
			blocked++
		}
		if q.Source != SourceName || q.Domain == "" || q.QType == "" {
			t.Fatalf("incomplete record %+v", q)
		}
	}
	if perDay["Living Room TV"] < 2000 {
		t.Errorf("Living Room TV %v/day, want thousands", perDay["Living Room TV"])
	}
	if hue := perDay["Hue Bridge"]; hue < 10 || hue > 200 {
		t.Errorf("Hue Bridge %v/day, want tens", hue)
	}
	if share := float64(blocked) / float64(len(h.DNS)); share <= 0 || share > 0.1 {
		t.Errorf("blocked share %.3f, want a pinch", share)
	}

	if len(h.Devices) != 10 {
		t.Errorf("%d devices, want 10", len(h.Devices))
	}
	for _, d := range h.Devices {
		if d.LastSeen.Before(d.FirstSeen) || d.FirstSeen.Before(from) || !d.LastSeen.Before(end) {
			t.Errorf("%s seen %v–%v outside the period", d.Label, d.FirstSeen, d.LastSeen)
		}
		if d.ID != "mac:"+d.MAC {
			t.Errorf("%s: ID %q does not match MAC", d.Label, d.ID)
		}
	}
}

func TestTVOvernightCadence(t *testing.T) {
	h := Generate(7, end, 7)
	var tv model.Device
	for _, d := range h.Devices {
		if d.Label == "Living Room TV" {
			tv = d
		}
	}
	var times []time.Time
	for _, q := range h.DNS {
		if q.ClientIP == tv.IPs[0] && q.Domain == "acr-us-prd.samsungcloud.tv" {
			times = append(times, q.Time)
		}
	}
	overnight := 0
	for _, ts := range times {
		if hr := ts.Hour(); hr >= 1 && hr < 6 {
			overnight++
		}
	}
	// One a minute, five hours a night, seven nights.
	if want := 7 * 5 * 60; overnight < want*9/10 || overnight > want*11/10 {
		t.Errorf("overnight ACR lookups = %d, want ≈%d", overnight, want)
	}
	for i := 1; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < 50*time.Second || gap > 70*time.Second {
			t.Fatalf("ACR gap %v at %v, want a steady ~1m", gap, times[i])
		}
	}

	if len(h.Flows) == 0 {
		t.Fatal("no DoH flows")
	}
	for _, f := range h.Flows {
		if f.ClientIP != tv.IPs[0] || f.RemoteIP.String() != "8.8.8.8" || f.RemotePort != 443 {
			t.Errorf("unexpected flow %+v", f)
		}
	}
}

func TestShortPeriod(t *testing.T) {
	h := Generate(1, end, 0) // clamps to one day
	from := end.Add(-24 * time.Hour)
	for _, q := range h.DNS {
		if q.Time.Before(from) || !q.Time.Before(end) {
			t.Fatalf("lookup at %v outside one-day period", q.Time)
		}
	}
	if len(h.DNS) == 0 {
		t.Fatal("no lookups")
	}
}

// The demo keeps a realistic share of lookups the knowledge base cannot
// classify, but only for hostnames that genuinely lack a sourced KB rule.
// Every other demo hostname must be classified, so a KB regression or a new
// unsourced demo hostname shows up here. Remove a name from this list when a
// rule with evidence lands for it.
var unclassifiedOnPurpose = []string{
	"dmls-na.amazon.com",
	"unagi-na.amazon.com",
}

func TestDemoDomainsClassified(t *testing.T) {
	k := kb.Default()
	h := Generate(42, end, 7)
	seen := map[string]bool{}
	var unknown []string
	for _, q := range h.DNS {
		if seen[q.Domain] {
			continue
		}
		seen[q.Domain] = true
		if k.Classify(q.Domain).Category == model.CatUnknown {
			unknown = append(unknown, q.Domain)
		}
	}
	slices.Sort(unknown)
	if !slices.Equal(unknown, unclassifiedOnPurpose) {
		t.Errorf("unclassified demo hostnames = %v\nwant %v", unknown, unclassifiedOnPurpose)
	}
}
