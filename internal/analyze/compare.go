package analyze

import (
	"slices"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// minPreviousCoverage is the share of the previous period that must hold
// stored data before phonehome compares with it. Below this the "before"
// figure would rest on too little to be fair to either side.
const minPreviousCoverage = 0.5

// partialSlack is how late stored data may begin and still count as covering
// the whole previous period (reports round their periods to the minute).
const partialSlack = time.Minute

// AnalyzeCompared is Analyze plus a comparison with the period of equal
// length just before p (p.Previous()), in HomeReport.Previous and each
// DeviceReport.Previous.
//
// qs and fl should hold the records of both periods, so one read from the
// store serves both; records outside them are ignored and each domain is
// classified once. dataFrom is when stored data begins (the oldest lookup
// kept, model.Status.Oldest). The comparison is left out (Previous nil) when
// that is unknown (zero) or later than p.From, when less than half of the
// previous period has data, or when the previous period has no lookups or
// connections at all. Rates in the previous period are per day of stored
// data in it; when data begins inside it the comparison is marked Partial.
// See docs/grading.md, "Compared with the previous period".
func AnalyzeCompared(c Classifier, p model.Period, devs []model.Device, qs []model.DNSQuery, fl []model.Flow, o Options, dataFrom time.Time) model.HomeReport {
	o = o.normalized()
	prevP := p.Previous()
	coverFrom, ok := previousCoverage(p, dataFrom)
	if !ok {
		return Analyze(c, p, devs, qs, fl, o)
	}

	cur := newAnalysis(c, p, devs, o)
	prev := newAnalysis(c, prevP, devs, o)
	prev.cache = cur.cache // classify each domain once across both periods
	prev.noFixes = true
	for q := range byTime(qs) {
		if cur.inPeriod(q.Time) {
			cur.addQuery(q)
		} else {
			prev.addQuery(q)
		}
	}
	for i := range fl {
		if cur.inPeriod(fl[i].Start) {
			cur.addFlow(&fl[i])
		} else {
			prev.addFlow(&fl[i])
		}
	}
	// The previous period is rated per day of stored data in it, not from
	// its first lookup: a quiet start is part of the record.
	prev.first = coverFrom

	hr := cur.report()
	before := prev.report()
	if len(before.Devices) == 0 {
		return hr // an empty previous period is no baseline
	}
	compare(&hr, before, cur.covered().Days(), prev.covered().Days(), coverFrom.After(prevP.From.Add(partialSlack)))
	return hr
}

// previousCoverage reports where data in p's previous period begins and
// whether enough of that period is covered to compare with.
func previousCoverage(p model.Period, dataFrom time.Time) (time.Time, bool) {
	prevP := p.Previous()
	if dataFrom.IsZero() || !dataFrom.Before(p.From) || !prevP.From.Before(prevP.To) {
		return time.Time{}, false
	}
	from := prevP.From
	if dataFrom.After(from) {
		from = dataFrom
	}
	if float64(prevP.To.Sub(from)) < minPreviousCoverage*float64(prevP.To.Sub(prevP.From)) {
		return time.Time{}, false
	}
	return from, true
}

// compare fills hr.Previous and each device's Previous from the report of the
// previous period. nowDays and prevDays are the days of data in each.
func compare(hr *model.HomeReport, before model.HomeReport, nowDays, prevDays float64, partial bool) {
	base := model.Comparison{
		Period:  before.Period,
		Days:    prevDays,
		Partial: partial,
	}
	home := &model.HomeComparison{Comparison: base, Devices: len(before.Devices)}
	home.Seen = true
	home.Total = before.Total
	home.Snooping = before.Snooping
	home.PerDay = float64(before.Snooping) / prevDays
	home.NowPerDay = float64(hr.Snooping) / nowDays
	home.Grade = before.Grade
	home.ByCategory = before.ByCategory
	home.CategoryDelta = categoryDelta(hr.ByCategory, nowDays, before.ByCategory, prevDays)

	was := make(map[string]*model.DeviceReport, len(before.Devices))
	for i := range before.Devices {
		was[before.Devices[i].Device.ID] = &before.Devices[i]
	}
	for i := range hr.Devices {
		d := &hr.Devices[i]
		c := base
		c.NowPerDay = d.PerDay
		c.ByCategory = zeroCategories()
		if old := was[d.Device.ID]; old != nil {
			delete(was, d.Device.ID)
			c.Seen = true
			c.Total = old.Total
			c.Snooping = old.Snooping
			c.PerDay = old.PerDay
			c.Grade = old.Grade
			c.ByCategory = old.ByCategory
			c.Stopped, c.Started = heartbeatChanges(old.Heartbeats, d.Heartbeats)
		}
		c.CategoryDelta = categoryDelta(d.ByCategory, nowDays, c.ByCategory, prevDays)
		d.Previous = &c
	}
	for _, d := range before.Devices { // keeps the worst-first order
		if was[d.Device.ID] != nil {
			home.Gone = append(home.Gone, d)
		}
	}
	hr.Previous = home
}

// categoryDelta is the change in lookups per day for every category.
func categoryDelta(now map[model.Category]int, nowDays float64, before map[model.Category]int, prevDays float64) map[model.Category]float64 {
	out := make(map[model.Category]float64, len(model.Categories()))
	for _, c := range model.Categories() {
		out[c] = float64(now[c])/nowDays - float64(before[c])/prevDays
	}
	return out
}

// heartbeatChanges lists the snooping heartbeats in before whose domain has
// none in now (stopped) and the reverse (started), each in heartbeat order.
// Clocks for time sync or firmware checks are left out: they come and go
// near the detection threshold and say nothing about you.
func heartbeatChanges(before, now []model.Heartbeat) (stopped, started []model.Heartbeat) {
	has := func(hs []model.Heartbeat, domain string) bool {
		return slices.ContainsFunc(hs, func(h model.Heartbeat) bool { return h.Domain == domain })
	}
	for _, h := range before {
		if h.Category.Snooping() && !has(now, h.Domain) {
			stopped = append(stopped, h)
		}
	}
	for _, h := range now {
		if h.Category.Snooping() && !has(before, h.Domain) {
			started = append(started, h)
		}
	}
	return stopped, started
}
