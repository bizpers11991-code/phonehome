package main

import (
	"context"
	"errors"
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
