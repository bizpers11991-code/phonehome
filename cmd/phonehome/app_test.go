package main

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/analyze"
	"github.com/bizpers11991-code/phonehome/internal/kb"
	"github.com/bizpers11991-code/phonehome/internal/model"
	"github.com/bizpers11991-code/phonehome/internal/web"
)

func TestSetLabelUnknownDevice(t *testing.T) {
	ctx := context.Background()
	st, err := loadDemo(ctx, 7, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := newApp(st, kb.Default(), analyze.DefaultOptions(), true)
	now := time.Now()
	r, err := a.Report(ctx, model.Period{From: now.AddDate(0, 0, -1), To: now})
	if err != nil || len(r.Devices) == 0 {
		t.Fatalf("report: %v", err)
	}
	if err := a.SetLabel(ctx, r.Devices[0].Device.ID, "Renamed"); err != nil {
		t.Fatalf("renaming a known device: %v", err)
	}
	if err := a.SetLabel(ctx, "mac:00:00:00:00:00:01", "x"); !errors.Is(err, web.ErrNotFound) {
		t.Fatalf("renaming an unknown device: err = %v, want ErrNotFound", err)
	}
	devs, _ := st.Devices(ctx)
	for _, d := range devs {
		if d.ID == "mac:00:00:00:00:00:01" {
			t.Fatal("an unknown id created a device")
		}
	}
}

// Concurrent dashboard requests for the same period must share one
// analysis: each holds the period's lookups in memory.
func TestReportSharesAnalysis(t *testing.T) {
	ctx := context.Background()
	st, err := loadDemo(ctx, 7, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := newApp(st, kb.Default(), analyze.DefaultOptions(), true)
	p := lastDays(3)
	var wg sync.WaitGroup
	totals := make([]int, 16)
	for i := range totals {
		wg.Go(func() {
			r, err := a.Report(ctx, p)
			if err != nil {
				t.Error(err)
			}
			totals[i] = r.Total
		})
	}
	wg.Wait()
	if n := a.analyses.Load(); n != 1 {
		t.Errorf("%d concurrent reports ran %d analyses, want 1", len(totals), n)
	}
	for _, n := range totals {
		if n != totals[0] || n == 0 {
			t.Fatalf("callers got different reports: totals %v", totals)
		}
	}

	// A change to the data starts a fresh analysis.
	a.invalidate()
	if _, err := a.Report(ctx, p); err != nil {
		t.Fatal(err)
	}
	if n := a.analyses.Load(); n != 2 {
		t.Errorf("after invalidate: %d analyses, want 2", n)
	}
}

// A caller that gives up must not fail the others waiting on its analysis:
// they run it again.
func TestReportLeaderCancelled(t *testing.T) {
	ctx := context.Background()
	st, err := loadDemo(ctx, 7, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := newApp(st, kb.Default(), analyze.DefaultOptions(), true)
	p := roundPeriod(lastDays(3))
	f := &flight{done: make(chan struct{}), err: context.Canceled}
	a.flights[p] = f
	go func() {
		a.mu.Lock()
		delete(a.flights, p)
		a.mu.Unlock()
		close(f.done)
	}()
	r, err := a.Report(ctx, p)
	if err != nil || r.Total == 0 {
		t.Fatalf("waiter got err %v, total %d; want a fresh analysis", err, r.Total)
	}
}

func TestReceiptCached(t *testing.T) {
	ctx := context.Background()
	st, err := loadDemo(ctx, 7, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := newApp(st, kb.Default(), analyze.DefaultOptions(), true)
	p := lastDays(1)
	one, err := a.Receipt(ctx, p, "", "svg")
	if err != nil {
		t.Fatal(err)
	}
	two, err := a.Receipt(ctx, p, "", "svg")
	if err != nil {
		t.Fatal(err)
	}
	if &one[0] != &two[0] {
		t.Error("the second receipt was rendered again")
	}
	if _, err := a.Receipt(ctx, p, "mac:00:00:00:00:00:01", "svg"); !errors.Is(err, web.ErrNotFound) {
		t.Errorf("unknown device: %v", err)
	}
	if len(a.receipts) != 1 {
		t.Errorf("%d receipts cached, want 1", len(a.receipts))
	}
}

func TestLoopback(t *testing.T) {
	for listen, want := range map[string]bool{
		"127.0.0.1:8099": true,
		"[::1]:8099":     true,
		"localhost:8099": true,
		":8099":          false,
		"0.0.0.0:8099":   false,
		"192.168.1.2:80": false,
		"[::]:8099":      false,
		"garbage":        false,
	} {
		if got := loopback(listen); got != want {
			t.Errorf("loopback(%q) = %t, want %t", listen, got, want)
		}
	}
}

// Report keeps each device's domain list until a budget of entries is
// spent, then leaves lists out (they are most of a report's memory when a
// household looks up very many names). ReportWithDomains lists one device's,
// and what it lists is what an unbudgeted analysis would.
func TestReportDomainsOnDemand(t *testing.T) {
	ctx := context.Background()
	st, err := loadDemo(ctx, 7, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := roundPeriod(lastDays(7))

	// An ordinary household is well inside the budget.
	roomy := newApp(st, kb.Default(), analyze.DefaultOptions(), true)
	r, err := roomy.Report(ctx, p)
	if err != nil || len(r.Devices) < 2 {
		t.Fatalf("report: %v, %d devices", err, len(r.Devices))
	}
	for _, d := range r.Devices {
		if len(d.Domains) == 0 || len(d.Domains) != d.DomainCount {
			t.Errorf("%s: %d domains of %d listed", d.Device.ID, len(d.Domains), d.DomainCount)
		}
	}
	full := r

	o := analyze.DefaultOptions()
	o.MaxDomainEntries = 1 // nothing fits
	a := newApp(st, kb.Default(), o, true)
	lean, err := a.Report(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range lean.Devices {
		if d.Domains != nil || len(d.TopDomains) == 0 || d.DomainCount != full.Devices[i].DomainCount {
			t.Errorf("%s: %d domains listed, %d top, %d counted", d.Device.ID, len(d.Domains), len(d.TopDomains), d.DomainCount)
		}
	}

	id := full.Devices[0].Device.ID
	got, err := a.ReportWithDomains(ctx, p, id)
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range got.Devices {
		if (d.Device.ID == id) != (d.Domains != nil) {
			t.Errorf("%s: %d domains listed", d.Device.ID, len(d.Domains))
		}
		if d.Device.ID == id && !reflect.DeepEqual(d.Domains, full.Devices[i].Domains) {
			t.Errorf("%s: domains differ from an unbudgeted analysis", id)
		}
		if !reflect.DeepEqual(d.TopDomains, full.Devices[i].TopDomains) || d.Total != full.Devices[i].Total {
			t.Errorf("%s: figures differ from an unbudgeted analysis", d.Device.ID)
		}
	}

	// Asking again is served from the cache; a change to the data is not.
	n := a.analyses.Load()
	if _, err := a.ReportWithDomains(ctx, p, id); err != nil || a.analyses.Load() != n {
		t.Errorf("second ReportWithDomains: err %v, %d analyses more", err, a.analyses.Load()-n)
	}
	a.invalidate()
	if _, err := a.ReportWithDomains(ctx, p, id); err != nil || a.analyses.Load() != n+1 {
		t.Errorf("after invalidate: err %v, %d analyses more, want 1", err, a.analyses.Load()-n)
	}
	// Only a couple are kept.
	for _, other := range full.Devices[1:] {
		if _, err := a.ReportWithDomains(ctx, p, other.Device.ID); err != nil {
			t.Fatal(err)
		}
	}
	a.mu.Lock()
	kept := len(a.domains)
	a.mu.Unlock()
	if kept > maxDomainReports {
		t.Errorf("%d domain reports cached, want at most %d", kept, maxDomainReports)
	}
}

// Concurrent requests for one device's full domain list share one analysis,
// as Report's do, and a list too long to keep in the cache is shared with
// those waiting but not kept for later.
func TestReportWithDomainsSharesAnalysis(t *testing.T) {
	ctx := context.Background()
	st, err := loadDemo(ctx, 7, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := newApp(st, kb.Default(), analyze.DefaultOptions(), true)
	p := lastDays(3)
	r, err := a.Report(ctx, p)
	if err != nil || len(r.Devices) == 0 {
		t.Fatalf("report: %v", err)
	}
	id := r.Devices[0].Device.ID
	base := a.analyses.Load()

	run := func() []int {
		var wg sync.WaitGroup
		counts := make([]int, 8)
		for i := range counts {
			wg.Go(func() {
				r, err := a.ReportWithDomains(ctx, p, id)
				if err != nil {
					t.Error(err)
					return
				}
				for _, d := range r.Devices {
					if d.Device.ID == id {
						counts[i] = len(d.Domains)
					}
				}
			})
		}
		wg.Wait()
		return counts
	}
	counts := run()
	if n := a.analyses.Load() - base; n != 1 {
		t.Errorf("8 concurrent ReportWithDomains ran %d analyses, want 1", n)
	}
	for _, n := range counts {
		if n == 0 || n != counts[0] {
			t.Fatalf("callers got different lists: %v", counts)
		}
	}
	// Short enough: kept for the next caller.
	if _, err := a.ReportWithDomains(ctx, p, id); err != nil || a.analyses.Load()-base != 1 {
		t.Errorf("repeat: err %v, %d analyses", err, a.analyses.Load()-base)
	}

	// Too long to keep: nothing is cached, concurrent callers still share.
	a.invalidate()
	a.domainCacheMax = 1
	base = a.analyses.Load()
	run()
	if n := a.analyses.Load() - base; n != 1 {
		t.Errorf("concurrent calls for an uncacheable list ran %d analyses, want 1", n)
	}
	a.mu.Lock()
	kept, flying := len(a.domains), len(a.domFlights)
	a.mu.Unlock()
	if kept != 0 || flying != 0 {
		t.Errorf("%d lists cached, %d flights left; want none", kept, flying)
	}
}
