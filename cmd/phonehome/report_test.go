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
	b.Reset()
	printReport(&b, r, 7)
	if strings.Contains(b.String(), "previous") {
		t.Errorf("report without a comparison mentions one:\n%s", b.String())
	}
}

// Each device says why it got its grade, whether the grade is provisional,
// and how much of its traffic the knowledge base recognised.
func TestPrintReportReason(t *testing.T) {
	to := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	r := model.HomeReport{
		Period: model.Period{From: to.Add(-7 * 24 * time.Hour), To: to}, Grade: "D",
		Devices: []model.DeviceReport{
			{Device: model.Device{ID: "a", Label: "TV", Kind: model.KindTV}, PerDay: 80, Grade: "D",
				Reason: model.GradeReason{Rule: model.ReasonBypass, PerDay: 80, VolumeGrade: "B", BandFrom: 50, BandTo: 300, Evidence: "8.8.8.8:53"},
				Total:  1000, ByCategory: map[model.Category]int{model.CatAds: 300, model.CatUnknown: 700}},
			{Device: model.Device{ID: "b", Label: "Phone", Kind: model.KindPhone}, PerDay: 3, Grade: "A",
				Reason: model.GradeReason{Rule: model.ReasonVolume, PerDay: 3, VolumeGrade: "A", BandTo: 50, Provisional: true, Data: 3 * time.Hour},
				Total:  10, ByCategory: map[model.Category]int{model.CatContent: 10}},
		},
	}
	var b strings.Builder
	printReport(&b, r, 7)
	out := b.String()
	for _, want := range []string{
		"     why: Bypasses your DNS via 8.8.8.8:53: at least D (its 80 snooping lookups a day alone would be B)\n" +
			"     Graded on the 30% of lookups phonehome recognises\n" +
			"     note: No known content-recognition server seen; not every TV brand's servers are known.\n",
		"     why: 3 snooping lookups a day (A is under 50)\n" +
			"     Provisional: based on 3 hours of data\n" +
			"     recognised: 100% of lookups\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q\n%s", want, out)
		}
	}
	if strings.Count(out, "note: No known content-recognition") != 1 {
		t.Errorf("ACR note not on the TV alone:\n%s", out)
	}
}
