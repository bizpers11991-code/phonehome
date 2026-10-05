// Package analyze turns raw DNS lookups and connection records into the
// findings phonehome shows people: which device talks to whom, how much of
// that is about the household rather than for it, and the patterns (clockwork
// heartbeats, night-time chatter, DNS bypass) that make the case.
//
// The detectors and their limits are described in docs/detectors.md; the
// grade scale in docs/grading.md.
package analyze

import (
	"cmp"
	"fmt"
	"iter"
	"math"
	"net/netip"
	"slices"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// Default option values, used when a field is left at its zero value.
const (
	DefaultQuietStart   = 1
	DefaultQuietEnd     = 6
	DefaultMinHeartbeat = 20
)

// maxTopDomains caps DeviceReport.TopDomains.
const maxTopDomains = 25

// now is the clock behind HomeReport.GeneratedAt; tests replace it.
var now = time.Now

// Options tunes Analyze. The zero value is ready to use and equivalent to
// DefaultOptions().
type Options struct {
	// QuietStart and QuietEnd bound the quiet-hours window [QuietStart,
	// QuietEnd) in local hours 0–23. The window wraps past midnight when
	// QuietStart > QuietEnd: 23 and 6 mean 23:00–06:00, 22 and 0 mean
	// 22:00–midnight. A window must be non-empty, so equal values (including
	// the zero value 0, 0) or hours outside 0–23 select the default 01:00–06:00.
	QuietStart, QuietEnd int
	// Location is the time zone for hourly and quiet-hours counts; nil means
	// time.Local.
	Location *time.Location
	// MinHeartbeat is the minimum number of distinct lookup moments a
	// (device, domain) pair needs before it is tested for a heartbeat;
	// zero or negative means DefaultMinHeartbeat.
	MinHeartbeat int
	// Resolvers are the household's own DNS servers (the Pi-hole or AdGuard
	// Home box). Their flows are exempt from connection-based DNS-bypass
	// findings, since forwarding lookups upstream is their job; they are
	// otherwise analysed like any device.
	Resolvers []netip.Addr
}

// DefaultOptions returns the defaults spelled out: quiet hours 01:00–06:00 in
// time.Local and a 20-lookup heartbeat minimum.
func DefaultOptions() Options {
	return Options{
		QuietStart:   DefaultQuietStart,
		QuietEnd:     DefaultQuietEnd,
		Location:     time.Local,
		MinHeartbeat: DefaultMinHeartbeat,
	}
}

func (o Options) normalized() Options {
	validHour := func(h int) bool { return h >= 0 && h < 24 }
	if o.QuietStart == o.QuietEnd || !validHour(o.QuietStart) || !validHour(o.QuietEnd) {
		o.QuietStart, o.QuietEnd = DefaultQuietStart, DefaultQuietEnd
	}
	if o.Location == nil {
		o.Location = time.Local
	}
	if o.MinHeartbeat <= 0 {
		o.MinHeartbeat = DefaultMinHeartbeat
	}
	return o
}

// quiet reports whether local hour h falls inside the quiet-hours window.
func (o Options) quiet(h int) bool {
	if o.QuietStart < o.QuietEnd {
		return h >= o.QuietStart && h < o.QuietEnd
	}
	return h >= o.QuietStart || h < o.QuietEnd
}

func (o Options) quietLabel() string {
	return fmt.Sprintf("%02d:00–%02d:00", o.QuietStart, o.QuietEnd)
}

// Classifier is what analyze needs from the knowledge base. *kb.KB satisfies
// it; analyze deliberately does not import kb.
type Classifier interface {
	Classify(domain string) model.Classification
	FixesFor(d model.Device, companyIDs []string) []model.Fix
}

// Analyze builds the report for period p.
//
// Lookups and flows are attributed to devices by client IP. When several
// devices claim the same IP, the one seen most recently (latest LastSeen) wins.
// Client IPs that belong to no device become synthetic devices with ID
// "ip:<addr>". Only records whose time lies in [p.From, p.To) count, and
// devices with neither lookups nor flows in the period are left out.
//
// Repeated queries for one name by one device within countWindow count as
// one lookup (see addQuery). qs may be in any order.
//
// c must not be nil. Each distinct domain is classified once.
func Analyze(c Classifier, p model.Period, devs []model.Device, qs []model.DNSQuery, fl []model.Flow, o Options) model.HomeReport {
	a := newAnalysis(c, p, devs, o.normalized())
	for q := range byTime(qs) {
		a.addQuery(q)
	}
	for i := range fl {
		a.addFlow(&fl[i])
	}
	return a.report()
}

// byTime yields qs oldest first, as addQuery needs. The store already returns
// lookups in time order (DNSBetween), so that is one check and one pass;
// other input (merged sources, tests) is walked through a sorted index, never
// reordered in place. Equal times keep their input order.
func byTime(qs []model.DNSQuery) iter.Seq[*model.DNSQuery] {
	return func(yield func(*model.DNSQuery) bool) {
		cmpTime := func(x, y *model.DNSQuery) int { return x.Time.Compare(y.Time) }
		sorted := true
		for i := 1; i < len(qs) && sorted; i++ {
			sorted = cmpTime(&qs[i-1], &qs[i]) <= 0
		}
		if sorted {
			for i := range qs {
				if !yield(&qs[i]) {
					return
				}
			}
			return
		}
		idx := make([]int, len(qs))
		for i := range idx {
			idx[i] = i
		}
		slices.SortStableFunc(idx, func(x, y int) int { return cmpTime(&qs[x], &qs[y]) })
		for _, i := range idx {
			if !yield(&qs[i]) {
				return
			}
		}
	}
}

// domainInfo is the per-run cache entry for one distinct domain.
type domainInfo struct {
	name     string
	cls      model.Classification
	resolver bool // a known encrypted-DNS resolver hostname
}

// domainAcc accumulates one device's lookups of one domain.
type domainAcc struct {
	info    *domainInfo
	count   int // lookups, each standing for its queries within countWindow
	blocked int // lookups with at least one blocked query
	// times are the starts of query bursts (burstWindow), oldest first: the
	// moments detectHeartbeat would merge them into anyway.
	times []int64 // unix nanoseconds
	first int64   // first and last query
	last  int64
	// counted is when the latest lookup began; countedBlocked whether any of
	// its queries so far was blocked.
	counted        int64
	countedBlocked bool
}

type deviceAcc struct {
	dev     model.Device
	total   int
	blocked int
	quiet   int
	flows   int
	hourly  [24]int
	domains map[*domainInfo]*domainAcc
	// bypass counts evidence per bypass kind: kind → evidence → occurrences.
	bypass map[string]map[string]int
	// first and last are the observed extremes, used for synthetic devices.
	first, last time.Time
}

func (d *deviceAcc) active() bool { return d.total > 0 || d.flows > 0 }

func (d *deviceAcc) seen(t time.Time) {
	if d.first.IsZero() || t.Before(d.first) {
		d.first = t
	}
	if t.After(d.last) {
		d.last = t
	}
}

func (d *deviceAcc) addBypass(kind, evidence string, n int) {
	if d.bypass == nil {
		d.bypass = make(map[string]map[string]int)
	}
	m := d.bypass[kind]
	if m == nil {
		m = make(map[string]int)
		d.bypass[kind] = m
	}
	m[evidence] += n
}

type analysis struct {
	c       Classifier
	p       model.Period
	o       Options
	byIP    map[netip.Addr]*deviceAcc
	devices []*deviceAcc
	cache   map[string]*domainInfo
	// resolvers is Options.Resolvers as a set of normalized addresses.
	resolvers map[netip.Addr]bool
	// first is the earliest lookup in the period: rates are per day of data,
	// so a fresh install viewing "30 days" is not graded on 2 days' traffic.
	first time.Time
	// noFixes skips looking up fixes: the previous period of a comparison
	// only needs its figures and grades.
	noFixes bool
}

// normAddr strips IPv6 zones and IPv4-in-IPv6 mapping so the same host always
// compares equal.
func normAddr(a netip.Addr) netip.Addr { return a.Unmap().WithZone("") }

func newAnalysis(c Classifier, p model.Period, devs []model.Device, o Options) *analysis {
	a := &analysis{
		c:         c,
		p:         p,
		o:         o,
		byIP:      make(map[netip.Addr]*deviceAcc),
		cache:     make(map[string]*domainInfo),
		resolvers: make(map[netip.Addr]bool, len(o.Resolvers)),
	}
	for _, r := range o.Resolvers {
		a.resolvers[normAddr(r)] = true
	}
	for _, d := range devs {
		acc := &deviceAcc{dev: d, domains: make(map[*domainInfo]*domainAcc)}
		a.devices = append(a.devices, acc)
		for _, ip := range d.IPs {
			ip = normAddr(ip)
			if cur, ok := a.byIP[ip]; ok && !ownsOver(d, cur.dev) {
				continue
			}
			a.byIP[ip] = acc
		}
	}
	return a
}

// ownsOver reports whether d should own a shared IP rather than cur: the
// device seen most recently wins; ties go to the smaller ID so the result does
// not depend on input order.
func ownsOver(d, cur model.Device) bool {
	if !d.LastSeen.Equal(cur.LastSeen) {
		return d.LastSeen.After(cur.LastSeen)
	}
	return d.ID < cur.ID
}

// covered is the part of the period that has data: from the first lookup
// (or the period start, if earlier data exists) to the period end.
func (a *analysis) covered() model.Period {
	p := a.p
	if a.first.After(p.From) {
		p.From = a.first
	}
	return p
}

func (a *analysis) inPeriod(t time.Time) bool {
	return !t.Before(a.p.From) && t.Before(a.p.To)
}

// device returns the accumulator owning ip, creating a synthetic device for
// an address no known device claims.
func (a *analysis) device(ip netip.Addr) *deviceAcc {
	ip = normAddr(ip)
	if d, ok := a.byIP[ip]; ok {
		return d
	}
	d := &deviceAcc{
		dev: model.Device{
			ID:   "ip:" + ip.String(),
			IPs:  []netip.Addr{ip},
			Kind: model.KindUnknown,
		},
		domains: make(map[*domainInfo]*domainAcc),
	}
	a.byIP[ip] = d
	a.devices = append(a.devices, d)
	return d
}

func (a *analysis) domain(name string) *domainInfo {
	if info, ok := a.cache[name]; ok {
		return info
	}
	cls := a.c.Classify(name)
	if !cls.Category.Valid() {
		cls.Category = model.CatUnknown
	}
	info := &domainInfo{name: name, cls: cls, resolver: isResolverHost(name)}
	a.cache[name] = info
	return info
}

// countWindow is how long after a counted lookup further queries for the
// same name by the same device are the same lookup rather than a new one.
//
// One lookup becomes several queries in two common ways. Apple devices and
// Chrome ask for A, AAAA and HTTPS records together, so counting queries
// made them look up to three times worse than a device asking for A alone.
// And Pi-hole answers blocked names with a 2-second TTL, so a device retrying
// a blocked tracker queries it again and again: counting queries made
// blocking a tracker worsen the device's grade. Both happen within seconds.
//
// The window is 30 seconds rather than a minute because steady clocks near
// the window are counted erratically: a beat landing just inside it is
// absorbed. Samsung's content recognition calls about once a minute, and a
// 60-second window drops every beat that comes a little early: about a
// quarter of them at 4% jitter. At 30 seconds a once-a-minute clock is
// counted in full with up to ±50% jitter. Faster clocks (LG's ACR, about
// every 15 s) count at most twice a minute. Heartbeat detection does not
// depend on the window: it sees every burst (domainAcc.times), as before.
const countWindow = 30 * time.Second

// addQuery adds one stored query. Queries must arrive oldest first (byTime).
// A query for a name the device already looked up less than countWindow ago
// adds nothing but its time and, if blocked, marks that lookup blocked; every
// count derived from lookups (totals, categories, hours, domains, blocked)
// therefore counts lookups, not queries.
func (a *analysis) addQuery(q *model.DNSQuery) {
	if !a.inPeriod(q.Time) {
		return
	}
	if a.first.IsZero() || q.Time.Before(a.first) {
		a.first = q.Time
	}
	d := a.device(q.ClientIP)
	d.seen(q.Time)

	info := a.domain(q.Domain)
	ns := q.Time.UnixNano()
	da := d.domains[info]
	if da == nil {
		da = &domainAcc{info: info, first: ns}
		d.domains[info] = da
	}
	da.last = ns
	if n := len(da.times); n == 0 || time.Duration(ns-da.times[n-1]) > burstWindow {
		da.times = append(da.times, ns)
	}
	if da.count > 0 && time.Duration(ns-da.counted) < countWindow {
		if q.Blocked && !da.countedBlocked {
			da.countedBlocked = true
			d.blocked++
			da.blocked++
		}
		return
	}

	da.count++
	da.counted, da.countedBlocked = ns, q.Blocked
	d.total++
	h := q.Time.In(a.o.Location).Hour()
	d.hourly[h]++
	if a.o.quiet(h) {
		d.quiet++
	}
	if q.Blocked {
		d.blocked++
		da.blocked++
	}
}

func (a *analysis) addFlow(f *model.Flow) {
	if !a.inPeriod(f.Start) {
		return
	}
	d := a.device(f.ClientIP)
	d.seen(f.Start)
	d.flows++
	if a.resolvers[normAddr(f.ClientIP)] {
		return
	}
	if kind, evidence, ok := flowBypass(f); ok {
		d.addBypass(kind, evidence, 1)
	}
}

func (a *analysis) report() model.HomeReport {
	hr := model.HomeReport{
		Period:      a.p,
		GeneratedAt: now(),
		ByCategory:  zeroCategories(),
	}
	for _, d := range a.devices {
		if !d.active() {
			continue
		}
		r := a.deviceReport(d)
		hr.Devices = append(hr.Devices, r)
		hr.Total += r.Total
		hr.Snooping += r.Snooping
		for cat, n := range r.ByCategory {
			hr.ByCategory[cat] += n
		}
	}
	slices.SortFunc(hr.Devices, func(x, y model.DeviceReport) int {
		return cmp.Or(
			cmp.Compare(x.Grade, y.Grade)*-1, // "F" sorts after "A": worst first
			cmp.Compare(y.Snooping, x.Snooping),
			cmp.Compare(x.Device.DisplayName(), y.Device.DisplayName()),
			cmp.Compare(x.Device.ID, y.Device.ID),
		)
	})
	hr.Grade = HomeGrade(hr.Devices)
	return hr
}

func zeroCategories() map[model.Category]int {
	m := make(map[model.Category]int, len(model.Categories()))
	for _, c := range model.Categories() {
		m[c] = 0
	}
	return m
}

func (a *analysis) deviceReport(d *deviceAcc) model.DeviceReport {
	r := model.DeviceReport{
		Device:     d.dev,
		Period:     a.p,
		Total:      d.total,
		Blocked:    d.blocked,
		ByCategory: zeroCategories(),
		Hourly:     d.hourly,
		QuietHours: d.quiet,
		QuietLabel: a.o.quietLabel(),
		Flows:      d.flows,
	}
	if r.Device.FirstSeen.IsZero() {
		r.Device.FirstSeen = d.first
	}
	if r.Device.LastSeen.IsZero() {
		r.Device.LastSeen = d.last
	}

	hints := make(map[model.DeviceKind]int)
	companies := make(map[string]*model.CompanyStat)
	countries := make(map[string]int)
	domains := make([]model.DomainStat, 0, len(d.domains))
	for info, da := range d.domains {
		cls := &info.cls
		r.ByCategory[cls.Category] += da.count
		if cls.Category.Snooping() {
			r.Snooping += da.count
		}
		if cls.KindHint != "" && cls.KindHint != model.KindUnknown {
			hints[cls.KindHint] += da.count
		}
		ds := model.DomainStat{
			Domain:     info.name,
			Count:      da.count,
			Blocked:    da.blocked,
			Category:   cls.Category,
			Purpose:    cls.Purpose,
			Rule:       cls.Rule,
			Confidence: cls.Confidence,
			Evidence:   cls.Evidence,
		}
		if da.count > 0 {
			ds.First, ds.Last = time.Unix(0, da.first), time.Unix(0, da.last)
		}
		if co := cls.Company; co != nil && co.ID != "" {
			ds.CompanyID, ds.CompanyName = co.ID, co.Name
			cs := companies[co.ID]
			if cs == nil {
				cs = &model.CompanyStat{ID: co.ID, Name: co.Name, Country: co.Country}
				companies[co.ID] = cs
			}
			cs.Count += da.count
			if co.Country != "" {
				countries[co.Country] += da.count
			}
		}
		domains = append(domains, ds)
		if cls.Category == model.CatUnknown && reportable(info.name) {
			r.Unknown = append(r.Unknown, unknownDomain(info.name, da))
		}
		if info.resolver {
			d.addBypass(BypassDoHLookup, info.name, da.count)
		}
		// Local names and reverse lookups are left out, as in Unknown: a clock
		// on the home network is no privacy finding, and receipts print
		// heartbeat names.
		if every, jitter, ok := detectHeartbeat(da.times, a.o.MinHeartbeat); ok && reportable(info.name) {
			r.Heartbeats = append(r.Heartbeats, model.Heartbeat{
				Domain:   info.name,
				Category: cls.Category,
				Count:    da.count,
				Every:    every,
				Jitter:   jitter,
			})
		}
	}
	if r.Total > 0 {
		r.SnoopShare = float64(r.Snooping) / float64(r.Total)
	}
	r.PerDay = float64(r.Snooping) / a.covered().Days()

	slices.SortFunc(domains, func(x, y model.DomainStat) int {
		return cmp.Or(cmp.Compare(y.Count, x.Count), cmp.Compare(x.Domain, y.Domain))
	})
	r.Domains = domains
	r.TopDomains = domains[:min(len(domains), maxTopDomains):min(len(domains), maxTopDomains)]
	sortHeartbeats(r.Heartbeats)
	r.Unknown = topUnknown(r.Unknown)

	for _, cs := range companies {
		r.Companies = append(r.Companies, *cs)
	}
	slices.SortFunc(r.Companies, func(x, y model.CompanyStat) int {
		return cmp.Or(cmp.Compare(y.Count, x.Count), cmp.Compare(x.ID, y.ID))
	})
	for code := range countries {
		r.Countries = append(r.Countries, code)
	}
	slices.SortFunc(r.Countries, func(x, y string) int {
		return cmp.Or(cmp.Compare(countries[y], countries[x]), cmp.Compare(x, y))
	})

	r.Bypasses = bypasses(d.bypass)
	r.Device.Kind = InferKind(r.Device, hints)

	if !a.noFixes {
		ids := make([]string, len(r.Companies))
		for i, cs := range r.Companies {
			ids[i] = cs.ID
		}
		r.Fixes = relevantFixes(a.c.FixesFor(r.Device, ids), r)
	}
	r.Grade = Grade(r)
	return r
}

// relevantFixes drops fixes whose When conditions r does not meet (a generic
// "block content recognition" fix means nothing for a device without ACR) and
// orders the rest: brand fixes before generic ones, then by the most serious
// finding they address, so turning off ACR comes before ad settings.
func relevantFixes(fixes []model.Fix, r model.DeviceReport) []model.Fix {
	type ranked struct {
		fix      model.Fix
		severity int
	}
	var keep []ranked
	for _, f := range fixes {
		sev, ok := fixSeverity(f, r)
		if ok {
			keep = append(keep, ranked{f, sev})
		}
	}
	slices.SortStableFunc(keep, func(a, b ranked) int {
		return cmp.Or(
			cmp.Compare(min(len(b.fix.Vendors), 1), min(len(a.fix.Vendors), 1)),
			cmp.Compare(a.severity, b.severity),
		)
	})
	out := make([]model.Fix, len(keep))
	for i, k := range keep {
		out[i] = k.fix
	}
	return out
}

// fixSeverity reports whether f applies to r and, if so, how serious the most
// serious finding it addresses is (lower is worse). Unconditional fixes rank
// after every targeted one.
func fixSeverity(f model.Fix, r model.DeviceReport) (int, bool) {
	if len(f.When) == 0 {
		return math.MaxInt, true
	}
	best, ok := math.MaxInt, false
	for _, w := range f.When {
		var sev int
		switch {
		case w == "bypass" && len(r.Bypasses) > 0:
			sev = 1 // between content recognition and advertising
		case w != "bypass" && r.ByCategory[model.Category(w)] > 0:
			sev = 2 * slices.Index(model.Categories(), model.Category(w))
		default:
			continue
		}
		best, ok = min(best, sev), true
	}
	return best, ok
}
