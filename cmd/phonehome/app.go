package main

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/analyze"
	"github.com/bizpers11991-code/phonehome/internal/config"
	"github.com/bizpers11991-code/phonehome/internal/kb"
	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/receipt"
	"github.com/bizpers11991-code/phonehome/internal/store"
	"github.com/bizpers11991-code/phonehome/internal/web"
)

// app answers the dashboard's questions from the store. It implements
// web.Backend.
type app struct {
	store *store.Store
	kb    *kb.KB
	demo  bool
	setup *setupState // nil: nothing to say about sources (demo, tests)

	mu       sync.Mutex
	opts     analyze.Options // replaced when serve re-detects sources
	gen      int             // bumped by invalidate; results of older generations are not cached
	cache    map[model.Period]cached
	flights  map[model.Period]*flight
	receipts map[receiptKey]cachedReceipt

	analysing chan struct{} // a slot per analysis running; see maxAnalyses
	analyses  atomic.Int64  // analyses run, for tests
}

type cached struct {
	report model.HomeReport
	at     time.Time
}

// flight is one analysis in progress; callers asking for the same period
// meanwhile wait for it instead of starting their own.
type flight struct {
	done   chan struct{} // closed when report and err are set
	report model.HomeReport
	err    error
}

type receiptKey struct {
	p        model.Period
	deviceID string
	format   string
}

type cachedReceipt struct {
	img []byte
	at  time.Time
}

// reportTTL bounds how stale the dashboard may be. Analysing a week on a
// Raspberry Pi takes a second or two; every page load should not pay that.
const reportTTL = 30 * time.Second

// maxAnalyses bounds how many periods are analysed at once. Each analysis
// of a month holds that month's lookups in memory, and a small machine is
// better off doing them in turn.
const maxAnalyses = 2

func newApp(st *store.Store, k *kb.KB, o analyze.Options, demo bool) *app {
	return &app{
		store: st, kb: k, opts: o, demo: demo,
		cache:     map[model.Period]cached{},
		flights:   map[model.Period]*flight{},
		receipts:  map[receiptKey]cachedReceipt{},
		analysing: make(chan struct{}, maxAnalyses),
	}
}

// roundPeriod rounds a period to the minute. Periods arrive as
// [now-days, now), so requests within the same minute share a cache entry.
func roundPeriod(p model.Period) model.Period {
	return model.Period{From: p.From.Truncate(time.Minute), To: p.To.Truncate(time.Minute)}
}

// Report analyses p, or returns the cached analysis. Concurrent callers
// asking for the same period share one analysis.
func (a *app) Report(ctx context.Context, p model.Period) (model.HomeReport, error) {
	p = roundPeriod(p)
	for {
		a.mu.Lock()
		if c, ok := a.cache[p]; ok && time.Since(c.at) < reportTTL {
			a.mu.Unlock()
			return c.report, nil
		}
		f := a.flights[p]
		if f == nil {
			f = &flight{done: make(chan struct{})}
			a.flights[p] = f
			gen, opts := a.gen, a.opts
			a.mu.Unlock()
			return a.lead(ctx, p, f, gen, opts)
		}
		a.mu.Unlock()
		select {
		case <-f.done:
		case <-ctx.Done():
			return model.HomeReport{}, ctx.Err()
		}
		if ctx.Err() == nil && (errors.Is(f.err, context.Canceled) || errors.Is(f.err, context.DeadlineExceeded)) {
			continue // the caller running it went away, not this one: try again
		}
		return f.report, f.err
	}
}

// lead runs the analysis for flight f, caches it and wakes those waiting.
func (a *app) lead(ctx context.Context, p model.Period, f *flight, gen int, opts analyze.Options) (model.HomeReport, error) {
	f.err = errors.New("analysis failed") // unless it returns
	defer func() {
		a.mu.Lock()
		if a.flights[p] == f {
			delete(a.flights, p)
		}
		if f.err == nil && gen == a.gen {
			for k, v := range a.cache { // keep the map from growing without bound
				if time.Since(v.at) >= reportTTL {
					delete(a.cache, k)
				}
			}
			a.cache[p] = cached{report: f.report, at: time.Now()}
		}
		a.mu.Unlock()
		close(f.done)
	}()
	f.report, f.err = a.analyse(ctx, p, opts)
	return f.report, f.err
}

// invalidate drops cached results, and makes analyses already running not
// cache theirs, after a change that makes them stale.
func (a *app) invalidate() {
	a.mu.Lock()
	a.gen++
	clear(a.cache)
	clear(a.flights)
	clear(a.receipts)
	a.mu.Unlock()
}

func (a *app) analyse(ctx context.Context, p model.Period, opts analyze.Options) (model.HomeReport, error) {
	select {
	case a.analysing <- struct{}{}:
		defer func() { <-a.analysing }()
	case <-ctx.Done():
		return model.HomeReport{}, ctx.Err()
	}
	a.analyses.Add(1)

	devs, err := a.store.Devices(ctx)
	if err != nil {
		return model.HomeReport{}, err
	}
	// When stored data reaches back before p, read the previous period too
	// (in the same query) so the report can say what changed. See
	// analyze.AnalyzeCompared for when a comparison is made.
	st, err := a.store.Status(ctx)
	if err != nil {
		return model.HomeReport{}, err
	}
	read := p
	if !st.Oldest.IsZero() && st.Oldest.Before(p.From) {
		read.From = p.Previous().From
	}
	qs, err := a.store.DNSBetween(ctx, read)
	if err != nil {
		return model.HomeReport{}, err
	}
	fl, err := a.store.FlowsBetween(ctx, read)
	if err != nil {
		return model.HomeReport{}, err
	}
	r := analyze.AnalyzeCompared(a.kb, p, devs, qs, fl, opts, st.Oldest)
	r.Demo = a.demo
	return r, nil
}

func (a *app) Status(ctx context.Context) (model.Status, error) {
	st, err := a.store.Status(ctx)
	st.Version = version
	st.Demo = a.demo
	st.Setup = a.statusSetup()
	return st, err
}

// statusSetup is how sources were chosen. Configured files are checked on
// every call, so a fixed permission or a newly created file shows at once.
func (a *app) statusSetup() model.Setup {
	s := a.setup.get()
	if !s.AutoDetect && len(s.Sources) > 0 {
		s.Problems = checkConfigured(s.Sources)
	}
	return s
}

// setOptions replaces the analysis options, e.g. when serve starts reading
// a source on this machine, and drops cached reports.
func (a *app) setOptions(o analyze.Options) {
	a.mu.Lock()
	a.opts = o
	a.mu.Unlock()
	a.invalidate()
}

// SetLabel names a device the store knows or the last 30 days' report
// shows (devices known only by IP exist only there). Any other id is
// ErrNotFound: the store would otherwise create a device row for it.
func (a *app) SetLabel(ctx context.Context, deviceID, label string) error {
	known, err := a.knownDevice(ctx, deviceID)
	if err != nil {
		return err
	}
	if !known {
		return web.ErrNotFound
	}
	if err := a.store.SetLabel(ctx, deviceID, label); err != nil {
		return err
	}
	a.invalidate()
	return nil
}

func (a *app) knownDevice(ctx context.Context, id string) (bool, error) {
	devs, err := a.store.Devices(ctx)
	if err != nil {
		return false, err
	}
	for _, d := range devs {
		if d.ID == id {
			return true, nil
		}
	}
	now := time.Now()
	r, err := a.Report(ctx, model.Period{From: now.AddDate(0, 0, -30), To: now})
	if err != nil {
		return false, err
	}
	for _, d := range r.Devices {
		if d.Device.ID == id {
			return true, nil
		}
	}
	return false, nil
}

// Receipt renders a receipt, or returns the one rendered for the same
// period, device and format within reportTTL.
func (a *app) Receipt(ctx context.Context, p model.Period, deviceID, format string) ([]byte, error) {
	k := receiptKey{roundPeriod(p), deviceID, format}
	a.mu.Lock()
	c, ok := a.receipts[k]
	gen := a.gen
	a.mu.Unlock()
	if ok && time.Since(c.at) < reportTTL {
		return c.img, nil
	}
	img, err := a.render(ctx, p, deviceID, format)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	if gen == a.gen {
		for k, v := range a.receipts {
			if time.Since(v.at) >= reportTTL {
				delete(a.receipts, k)
			}
		}
		a.receipts[k] = cachedReceipt{img: img, at: time.Now()}
	}
	a.mu.Unlock()
	return img, nil
}

func (a *app) render(ctx context.Context, p model.Period, deviceID, format string) ([]byte, error) {
	r, err := a.Report(ctx, p)
	if err != nil {
		return nil, err
	}
	o := receipt.Options{Demo: a.demo, Now: time.Now().In(a.location())}
	var doc receipt.Doc
	if deviceID == "" {
		doc = receipt.Home(r, o)
	} else {
		dr, ok := findDevice(r, deviceID)
		if !ok {
			return nil, fmt.Errorf("device %q: %w", deviceID, web.ErrNotFound)
		}
		doc = receipt.Device(dr, o)
	}
	switch format {
	case "svg":
		return doc.SVG(), nil
	case "png":
		return doc.PNG()
	}
	return nil, fmt.Errorf("unknown receipt format %q", format)
}

func (a *app) location() *time.Location {
	a.mu.Lock()
	loc := a.opts.Location
	a.mu.Unlock()
	if loc != nil {
		return loc
	}
	return time.Local
}

func findDevice(r model.HomeReport, id string) (model.DeviceReport, bool) {
	for _, d := range r.Devices {
		if d.Device.ID == id {
			return d, true
		}
	}
	return model.DeviceReport{}, false
}

// checkDays rejects a --days that would make an empty, inverted or
// overflowing period.
func checkDays(days int) error {
	if days < 1 || days > config.MaxRetentionDays {
		return fmt.Errorf("--days %d: use a number of days from 1 to %d", days, config.MaxRetentionDays)
	}
	return nil
}

// lastDays is the period [now-days, now).
func lastDays(days int) model.Period {
	now := time.Now()
	return model.Period{From: now.Add(-time.Duration(days) * 24 * time.Hour), To: now}
}

// hostAddrs lists this machine's own unicast addresses: when phonehome runs
// on the Pi-hole box, its upstream forwarding must not count as a bypass.
func hostAddrs() []netip.Addr {
	var out []netip.Addr
	for _, a := range interfaceAddrs() {
		if a.IsLoopback() || a.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, a)
	}
	return out
}
