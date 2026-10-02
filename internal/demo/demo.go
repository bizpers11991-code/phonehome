// Package demo generates a deterministic, entirely synthetic household so
// phonehome can be tried, screenshotted and documented without a Pi-hole.
//
// Nothing here was captured from a real network. The devices, MAC addresses,
// IPs and timings are invented; only the hostnames are real, chosen from
// published research and community blocklists (see domains.go) so the
// classifier has something realistic to chew on. Anything built from this
// data must be labelled as demo data (model.HomeReport.Demo).
package demo

import (
	"math/rand/v2"
	"net/netip"
	"slices"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// SourceName is the model.DNSQuery/Flow Source of every generated record.
const SourceName = "demo"

// Household is a synthetic home: its devices and what they did.
type Household struct {
	Devices []model.Device
	DNS     []model.DNSQuery // sorted by Time
	Flows   []model.Flow     // sorted by Start
}

// Generate builds a household covering [end-days*24h, end). The same seed,
// end and days always produce the same household. Local-time rhythms (TV in
// the evening, vacuum in the morning) follow end's location.
func Generate(seed int64, end time.Time, days int) Household {
	if days < 1 {
		days = 1
	}
	g := gen{
		from: end.Add(-time.Duration(days) * 24 * time.Hour),
		to:   end,
		loc:  end.Location(),
	}
	var h Household
	for i, d := range household() {
		g.rng = rand.New(rand.NewPCG(uint64(seed), uint64(i)+1))
		g.qs, g.flows = g.qs[:0], g.flows[:0]
		g.device(d)
		dev := d.device
		dev.FirstSeen = g.from
		dev.LastSeen = g.from
		for _, q := range g.qs {
			if q.Time.After(dev.LastSeen) {
				dev.LastSeen = q.Time
			}
		}
		h.Devices = append(h.Devices, dev)
		h.DNS = append(h.DNS, g.qs...)
		h.Flows = append(h.Flows, g.flows...)
	}
	slices.SortStableFunc(h.DNS, func(a, b model.DNSQuery) int { return a.Time.Compare(b.Time) })
	slices.SortStableFunc(h.Flows, func(a, b model.Flow) int { return a.Start.Compare(b.Start) })
	return h
}

// spec describes how one synthetic device behaves.
type spec struct {
	device model.Device
	// usage[h] is the chance the device is in active use during local hour h
	// on a weekday; weekend overrides it when set.
	usage, weekend *[24]float64
	perHour        int    // lookups per active hour, before ±40% jitter
	use            []dom  // what active use looks up, weighted
	beats          []beat // clockwork lookups
	qtypes         []string
}

// dom is a hostname with a relative weight within a device's active use.
type dom struct {
	name    string
	weight  int
	blocked bool // the demo resolver blocks it
}

// beat is a lookup on a fixed clock. If always is false it only runs while
// the device is in use.
type beat struct {
	name    string
	every   time.Duration
	jitter  float64 // ± fraction of every
	always  bool
	blocked bool
	doh     bool // also emit an outbound TCP/443 flow to Google Public DNS
}

type gen struct {
	from, to time.Time
	loc      *time.Location
	rng      *rand.Rand
	qs       []model.DNSQuery
	flows    []model.Flow
}

func (g *gen) device(s spec) {
	ip := s.device.IPs[0]
	emit := func(t time.Time, name string, blocked bool) {
		g.qs = append(g.qs, model.DNSQuery{
			Time:     t,
			ClientIP: ip,
			Domain:   name,
			QType:    s.qtypes[g.rng.IntN(len(s.qtypes))],
			Blocked:  blocked,
			Source:   SourceName,
		})
	}

	// Decide hour by hour whether the device is in use.
	first := g.from.Truncate(time.Hour)
	var on []bool
	for t := first; t.Before(g.to); t = t.Add(time.Hour) {
		lt := t.In(g.loc)
		p := s.usage
		if s.weekend != nil && (lt.Weekday() == time.Saturday || lt.Weekday() == time.Sunday) {
			p = s.weekend
		}
		on = append(on, g.rng.Float64() < p[lt.Hour()])
	}
	inUse := func(t time.Time) bool { return on[int(t.Sub(first)/time.Hour)] }

	total := 0
	for _, d := range s.use {
		total += d.weight
	}
	for i, active := range on {
		if !active || total == 0 {
			continue
		}
		start := first.Add(time.Duration(i) * time.Hour)
		n := int(float64(s.perHour) * (0.6 + 0.8*g.rng.Float64()))
		for range n {
			t := start.Add(time.Duration(g.rng.Int64N(int64(time.Hour))))
			if t.Before(g.from) || !t.Before(g.to) {
				continue
			}
			d := pick(g.rng, s.use, total)
			emit(t, d.name, d.blocked)
		}
	}

	for _, b := range s.beats {
		t := g.from.Add(time.Duration(g.rng.Int64N(int64(b.every))))
		for t.Before(g.to) {
			if b.always || inUse(t) {
				emit(t, b.name, b.blocked)
				if b.doh {
					g.dohFlow(ip, t)
				}
			}
			t = t.Add(time.Duration(float64(b.every) * (1 + b.jitter*(2*g.rng.Float64()-1))))
		}
	}
}

var googleDNS = netip.MustParseAddr("8.8.8.8")

// dohFlow records the HTTPS connection to Google Public DNS that follows a
// lookup of dns.google: the TV resolving names around the local resolver.
func (g *gen) dohFlow(client netip.Addr, t time.Time) {
	start := t.Add(time.Duration(200+g.rng.IntN(800)) * time.Millisecond)
	g.flows = append(g.flows, model.Flow{
		Start:      start,
		End:        start.Add(time.Duration(5+g.rng.IntN(55)) * time.Second),
		ClientIP:   client,
		RemoteIP:   googleDNS,
		RemotePort: 443,
		Proto:      "tcp",
		BytesOut:   uint64(2000 + g.rng.IntN(6000)),
		BytesIn:    uint64(4000 + g.rng.IntN(12000)),
		Source:     SourceName,
	})
}

func pick(r *rand.Rand, ds []dom, total int) dom {
	n := r.IntN(total)
	for _, d := range ds {
		if n < d.weight {
			return d
		}
		n -= d.weight
	}
	return ds[len(ds)-1]
}
