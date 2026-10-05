package analyze

import (
	"fmt"
	"math/rand/v2"
	"net/netip"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// benchKB classifies synthetic domains by their numeric suffix.
type benchKB struct{ co []*model.Company }

func (k benchKB) Classify(domain string) model.Classification {
	var n int
	fmt.Sscanf(domain, "d%d.example", &n)
	cats := model.Categories()
	return model.Classification{Domain: domain, Category: cats[n%len(cats)], Company: k.co[n%len(k.co)]}
}

func (benchKB) FixesFor(model.Device, []string) []model.Fix { return nil }

var benchClassifier = benchKB{co: []*model.Company{
	{ID: "a", Country: "US"}, {ID: "b", Country: "KR"}, {ID: "c", Country: "CN"},
}}

const (
	benchPerWeek = 1_000_000
	benchDevices = 20
	benchDomains = 5_000
)

// benchHousehold is a busy household over p: 1M lookups a week from 20
// devices to 5k domains, with every device running a few heartbeats.
func benchHousehold(p model.Period) ([]model.Device, []model.DNSQuery) {
	nQueries := int(float64(benchPerWeek) * p.Days() / 7)
	rng := rand.New(rand.NewPCG(7, 7))
	devs := make([]model.Device, benchDevices)
	for i := range devs {
		devs[i] = model.Device{
			ID:       fmt.Sprintf("mac:%02d", i),
			IPs:      []netip.Addr{netip.AddrFrom4([4]byte{10, 0, 0, byte(i + 1)})},
			Hostname: fmt.Sprintf("device-%02d", i),
		}
	}
	domains := make([]string, benchDomains)
	for i := range domains {
		domains[i] = fmt.Sprintf("d%d.example", i)
	}
	span := int64(p.To.Sub(p.From))
	qs := make([]model.DNSQuery, nQueries)
	for i := range qs {
		dev := i % benchDevices
		// Half the traffic is a per-device heartbeat on a few domains, the
		// rest is spread over the long tail.
		round := i / benchDevices
		var dom int
		if round%2 == 0 {
			dom = dev*3 + round/2%3
		} else {
			dom = rng.IntN(benchDomains)
		}
		qs[i] = model.DNSQuery{
			Time:     p.From.Add(time.Duration(int64(i) * (span / int64(nQueries)))),
			ClientIP: devs[dev].IPs[0],
			Domain:   domains[dom],
		}
	}
	return devs, qs
}

// BenchmarkAnalyze measures one week of the busy household.
func BenchmarkAnalyze(b *testing.B) {
	devs, qs := benchHousehold(week)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		r := Analyze(benchClassifier, week, devs, qs, nil, utcOpt)
		// A few hundred long-tail repeats fall within countWindow.
		if len(r.Devices) != benchDevices || r.Total < benchPerWeek*99/100 {
			b.Fatalf("got %d devices, %d lookups", len(r.Devices), r.Total)
		}
	}
}

// BenchmarkAnalyzeCompared measures the same week compared with the week
// before it: twice the lookups, one pass. It should cost about twice
// BenchmarkAnalyze, no more.
func BenchmarkAnalyzeCompared(b *testing.B) {
	p := model.Period{From: week.To, To: week.To.Add(week.To.Sub(week.From))}
	devs, qs := benchHousehold(model.Period{From: week.From, To: p.To})
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		r := AnalyzeCompared(benchClassifier, p, devs, qs, nil, utcOpt, week.From)
		if len(r.Devices) != benchDevices || r.Previous == nil || r.Total+r.Previous.Total < len(qs)*99/100 {
			b.Fatalf("got %d devices, %d lookups", len(r.Devices), r.Total)
		}
	}
}
