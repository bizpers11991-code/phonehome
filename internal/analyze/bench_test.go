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

// BenchmarkAnalyze measures a busy household: 1M lookups over 7 days from 20
// devices to 5k domains, with every device running a few heartbeats.
func BenchmarkAnalyze(b *testing.B) {
	const (
		nQueries = 1_000_000
		nDevices = 20
		nDomains = 5_000
	)
	rng := rand.New(rand.NewPCG(7, 7))
	devs := make([]model.Device, nDevices)
	for i := range devs {
		devs[i] = model.Device{
			ID:       fmt.Sprintf("mac:%02d", i),
			IPs:      []netip.Addr{netip.AddrFrom4([4]byte{10, 0, 0, byte(i + 1)})},
			Hostname: fmt.Sprintf("device-%02d", i),
		}
	}
	domains := make([]string, nDomains)
	for i := range domains {
		domains[i] = fmt.Sprintf("d%d.example", i)
	}
	span := int64(week.To.Sub(week.From))
	qs := make([]model.DNSQuery, nQueries)
	for i := range qs {
		dev := i % nDevices
		// Half the traffic is a per-device heartbeat on a few domains, the
		// rest is spread over the long tail.
		round := i / nDevices
		var dom int
		if round%2 == 0 {
			dom = dev*3 + round/2%3
		} else {
			dom = rng.IntN(nDomains)
		}
		qs[i] = model.DNSQuery{
			Time:     week.From.Add(time.Duration(int64(i) * (span / nQueries))),
			ClientIP: devs[dev].IPs[0],
			Domain:   domains[dom],
		}
	}
	kb := benchKB{co: []*model.Company{
		{ID: "a", Country: "US"}, {ID: "b", Country: "KR"}, {ID: "c", Country: "CN"},
	}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		r := Analyze(kb, week, devs, qs, nil, utcOpt)
		if len(r.Devices) != nDevices || r.Total != nQueries {
			b.Fatalf("got %d devices, %d lookups", len(r.Devices), r.Total)
		}
	}
}
