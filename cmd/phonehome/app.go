package main

import (
	"context"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/analyze"
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

	mu    sync.Mutex
	opts  analyze.Options // replaced when serve re-detects sources
	cache map[model.Period]cached
}

type cached struct {
	report model.HomeReport
	at     time.Time
}

// reportTTL bounds how stale the dashboard may be. Analysing a week on a
// Raspberry Pi takes a second or two; every page load should not pay that.
const reportTTL = 30 * time.Second

func newApp(st *store.Store, k *kb.KB, o analyze.Options, demo bool) *app {
	return &app{store: st, kb: k, opts: o, demo: demo, cache: map[model.Period]cached{}}
}

func (a *app) Report(ctx context.Context, p model.Period) (model.HomeReport, error) {
	// Periods arrive as [now-days, now); round so that requests within the
	// same minute share a cache entry.
	p = model.Period{From: p.From.Truncate(time.Minute), To: p.To.Truncate(time.Minute)}

	a.mu.Lock()
	c, ok := a.cache[p]
	opts := a.opts
	a.mu.Unlock()
	if ok && time.Since(c.at) < reportTTL {
		return c.report, nil
	}

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

	a.mu.Lock()
	for k, v := range a.cache { // keep the map from growing without bound
		if time.Since(v.at) >= reportTTL {
			delete(a.cache, k)
		}
	}
	a.cache[p] = cached{report: r, at: time.Now()}
	a.mu.Unlock()
	return r, nil
}

func (a *app) Status(ctx context.Context) (model.Status, error) {
	st, err := a.store.Status(ctx)
	st.Version = version
	st.Demo = a.demo
	st.Setup = a.setup.get()
	return st, err
}

// setOptions replaces the analysis options, e.g. when serve starts reading
// a source on this machine, and drops cached reports.
func (a *app) setOptions(o analyze.Options) {
	a.mu.Lock()
	a.opts = o
	clear(a.cache)
	a.mu.Unlock()
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
	a.mu.Lock()
	clear(a.cache)
	a.mu.Unlock()
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

func (a *app) Receipt(ctx context.Context, p model.Period, deviceID, format string) ([]byte, error) {
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
