package main

import (
	"strings"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

func TestPrintUnknown(t *testing.T) {
	t0 := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	r := model.HomeReport{Devices: []model.DeviceReport{
		{Device: model.Device{ID: "a", Hostname: "quiet-plug"}}, // nothing unknown: skipped
		{
			Device: model.Device{ID: "b", Label: "Den TV", Vendor: "Acme", Kind: model.KindTV},
			Unknown: []model.UnknownDomain{
				{Domain: "a.acme.example", Group: "acme.example", Count: 30, First: t0, Last: t0.Add(time.Hour)},
				{Domain: "solo.example", Group: "solo.example", Count: 20, First: t0, Last: t0},
				{Domain: "b.acme.example", Group: "acme.example", Count: 5, First: t0.Add(-time.Hour), Last: t0},
				{Domain: "x.other.example", Group: "other.example", Count: 1},
			},
		},
	}}
	var b strings.Builder
	printUnknown(&b, r, 7, 2, time.UTC)
	out := b.String()
	for _, want := range []string{
		"Den TV  (Acme · tv)  56 lookups",
		"35  Sep 30 07:00  Sep 30 09:00  acme.example  (2 names)",
		"30  Sep 30 08:00  Sep 30 09:00    a.acme.example",
		"20  Sep 30 08:00  Sep 30 08:00  solo.example\n",
		"… and 1 more",
		"issues/new?template=new-device.yml",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "quiet-plug") || strings.Contains(out, "x.other.example") {
		t.Errorf("output shows a device without unknowns or a group past the limit:\n%s", out)
	}

	b.Reset()
	printUnknown(&b, model.HomeReport{Devices: r.Devices[:1]}, 7, 10, time.UTC)
	if !strings.Contains(b.String(), "No unclassified domains") {
		t.Errorf("empty output = %q", b.String())
	}
}
