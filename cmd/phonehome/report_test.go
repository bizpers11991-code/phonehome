package main

import (
	"strings"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

func TestPrintReportComparison(t *testing.T) {
	to := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	p := model.Period{From: to.Add(-7 * 24 * time.Hour), To: to}
	prev := p.Previous()
	r := model.HomeReport{
		Period: p, Grade: "D",
		Devices: []model.DeviceReport{
			{Device: model.Device{ID: "a", Label: "TV"}, PerDay: 80, Grade: "D",
				Previous: &model.Comparison{Period: prev, Seen: true, PerDay: 1000, NowPerDay: 80, Grade: "F",
					Stopped: []model.Heartbeat{{Domain: "acr-us-prd.samsungcloud.tv", Category: model.CatACR}}}},
			{Device: model.Device{ID: "b", Label: "Phone"}, Grade: "A",
				Previous: &model.Comparison{Period: prev}},
		},
		Previous: &model.HomeComparison{
			Comparison: model.Comparison{Period: prev, Days: 3.5, Partial: true, Seen: true, PerDay: 1000, NowPerDay: 80, Grade: "F"},
			Gone:       []model.DeviceReport{{Device: model.Device{ID: "c", Label: "Lamp"}, Grade: "B"}},
		},
	}
	var b strings.Builder
	printReport(&b, r, 7)
	out := b.String()
	for _, want := range []string{
		"Compared with the previous 7 days: 1,000 → 80 snooping lookups/day (↓ 92%), home grade F → D.",
		"Only 3.5 of those 7 days have data",
		"Not seen this period: Lamp (was B)",
		"vs previous 7 days: 1,000 → 80 snooping/day (↓ 92%), grade F → D",
		"stopped: acr-us-prd.samsungcloud.tv heartbeat (Content recognition)",
		"new: not seen in the previous 7 days",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q\n%s", want, out)
		}
	}

	r.Previous = nil
	r.Devices[0].Previous, r.Devices[1].Previous = nil, nil
	r.Devices[0].Reason = model.GradeReason{Rule: model.ReasonBypass, PerDay: 80, VolumeGrade: "B", Evidence: "8.8.8.8:53"}
	r.Devices[0].Total, r.Devices[0].ByCategory = 1000, map[model.Category]int{model.CatAds: 300, model.CatUnknown: 700}
	r.Devices[1].Reason = model.GradeReason{Rule: model.ReasonVolume, PerDay: 3, VolumeGrade: "A", BandTo: 50, Provisional: true, Data: 3 * time.Hour}
	r.Devices[1].Total, r.Devices[1].ByCategory = 10, map[model.Category]int{model.CatContent: 10}
	b.Reset()
	printReport(&b, r, 7)
	out = b.String()
	for _, want := range []string{
		"why: Bypasses your DNS via 8.8.8.8:53: at least D (its 80 snooping lookups a day alone would be B)",
		"classified: 30% of lookups (grade based on those alone)",
		"why: 3 snooping lookups a day (A is under 50)",
		"Provisional: based on 3 hours of data",
		"classified: 100% of lookups\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q\n%s", want, out)
		}
	}
	b.Reset()
	printReport(&b, r, 7)
	if strings.Contains(b.String(), "previous") {
		t.Errorf("report without a comparison mentions one:\n%s", b.String())
	}
}
