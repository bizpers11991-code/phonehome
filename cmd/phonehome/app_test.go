package main

import (
	"context"
	"errors"
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
