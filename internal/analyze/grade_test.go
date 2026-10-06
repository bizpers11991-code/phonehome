package analyze

import (
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

func TestGrade(t *testing.T) {
	acrBeat := []model.Heartbeat{{Domain: "acr.example", Category: model.CatACR}}
	telBeat := []model.Heartbeat{{Domain: "log.example", Category: model.CatTelemetry}}
	high := []model.Bypass{{Kind: BypassDoTFlow, Confidence: "high"}}
	medium := []model.Bypass{{Kind: BypassDoHLookup, Confidence: "medium"}}
	acr := map[model.Category]int{model.CatACR: 1}
	tests := []struct {
		name string
		r    model.DeviceReport
		want string
	}{
		{"silent", model.DeviceReport{}, "A"},
		{"just under B", model.DeviceReport{PerDay: 49.9}, "A"},
		{"B", model.DeviceReport{PerDay: 50}, "B"},
		{"C", model.DeviceReport{PerDay: 300}, "C"},
		{"D by volume", model.DeviceReport{PerDay: 1500}, "D"},
		{"F by volume", model.DeviceReport{PerDay: 5000}, "F"},
		{"any ACR lookup is D", model.DeviceReport{ByCategory: acr}, "D"},
		{"ACR heartbeat is F", model.DeviceReport{Heartbeats: acrBeat, ByCategory: acr}, "F"},
		{"other heartbeat alone does not grade", model.DeviceReport{Heartbeats: telBeat}, "A"},
		{"high-confidence bypass is D", model.DeviceReport{Bypasses: high}, "D"},
		{"medium bypass does not grade", model.DeviceReport{Bypasses: medium, PerDay: 60}, "B"},
	}
	for _, tt := range tests {
		if got := Grade(tt.r); got != tt.want {
			t.Errorf("%s: Grade = %s, want %s", tt.name, got, tt.want)
		}
	}
}

func TestGradeWhy(t *testing.T) {
	tv := model.Device{Kind: model.KindTV}
	laptop := model.Device{Kind: model.KindComputer}
	acrBeat := []model.Heartbeat{{Domain: "acr.example", Category: model.CatACR, Every: 15 * time.Second}}
	high := []model.Bypass{{Kind: BypassDoHFlow, Evidence: "8.8.8.8:443", Confidence: "high"}}
	medium := []model.Bypass{{Kind: BypassDoHLookup, Evidence: "dns.google", Confidence: "medium"}}
	acr := map[model.Category]int{model.CatACR: 120}
	tests := []struct {
		name  string
		r     model.DeviceReport
		grade string
		why   model.GradeReason
		text  string
	}{
		{"quiet", model.DeviceReport{PerDay: 12},
			"A", model.GradeReason{Rule: model.ReasonVolume, PerDay: 12, VolumeGrade: "A", BandTo: 50},
			"12 snooping lookups a day (A is under 50)"},
		{"one a day", model.DeviceReport{PerDay: 1},
			"A", model.GradeReason{Rule: model.ReasonVolume, PerDay: 1, VolumeGrade: "A", BandTo: 50},
			"1 snooping lookup a day (A is under 50)"},
		{"B", model.DeviceReport{PerDay: 50},
			"B", model.GradeReason{Rule: model.ReasonVolume, PerDay: 50, VolumeGrade: "B", BandFrom: 50, BandTo: 300},
			"50 snooping lookups a day (B is 50–299)"},
		{"C by volume", model.DeviceReport{Device: tv, PerDay: 1359},
			"C", model.GradeReason{Rule: model.ReasonVolume, PerDay: 1359, VolumeGrade: "C", BandFrom: 300, BandTo: 1500},
			"1,359 snooping lookups a day (C is 300–1,499)"},
		{"D by volume", model.DeviceReport{PerDay: 1500},
			"D", model.GradeReason{Rule: model.ReasonVolume, PerDay: 1500, VolumeGrade: "D", BandFrom: 1500, BandTo: 5000},
			"1,500 snooping lookups a day (D is 1,500–4,999)"},
		{"F by volume", model.DeviceReport{PerDay: 6000, Bypasses: high},
			"F", model.GradeReason{Rule: model.ReasonVolume, PerDay: 6000, VolumeGrade: "F", BandFrom: 5000},
			"6,000 snooping lookups a day (F is 5,000 or more)"},
		{"bypass raises C to D", model.DeviceReport{Device: tv, PerDay: 568, Bypasses: append(medium, high...)},
			"D", model.GradeReason{Rule: model.ReasonBypass, PerDay: 568, VolumeGrade: "C", BandFrom: 300, BandTo: 1500, Evidence: "8.8.8.8:443"},
			"Bypasses your DNS via 8.8.8.8:443: at least D (its 568 snooping lookups a day alone would be C)"},
		{"medium bypass does not grade", model.DeviceReport{PerDay: 60, Bypasses: medium},
			"B", model.GradeReason{Rule: model.ReasonVolume, PerDay: 60, VolumeGrade: "B", BandFrom: 50, BandTo: 300},
			"60 snooping lookups a day (B is 50–299)"},
		{"ACR on a TV is D", model.DeviceReport{Device: tv, PerDay: 20, ByCategory: acr, Bypasses: high},
			"D", model.GradeReason{Rule: model.ReasonACR, PerDay: 20, VolumeGrade: "A", BandTo: 50, Count: 120},
			"Contacts content-recognition (ACR) servers (120 lookups): at least D"},
		{"ACR beats volume D", model.DeviceReport{PerDay: 2000, ByCategory: acr},
			"D", model.GradeReason{Rule: model.ReasonACR, PerDay: 2000, VolumeGrade: "D", BandFrom: 1500, BandTo: 5000, Count: 120},
			"Contacts content-recognition (ACR) servers (120 lookups): at least D"},
		{"ACR heartbeat on a TV is F", model.DeviceReport{Device: tv, PerDay: 300, ByCategory: acr, Heartbeats: acrBeat},
			"F", model.GradeReason{Rule: model.ReasonACRHeartbeat, PerDay: 300, VolumeGrade: "C", BandFrom: 300, BandTo: 1500, Evidence: "acr.example", Every: 15 * time.Second},
			"Content recognition (ACR) on a clock, every 15 s: always F"},
		{"ACR on a laptop is just snooping", model.DeviceReport{Device: laptop, PerDay: 20, ByCategory: acr, Heartbeats: acrBeat},
			"A", model.GradeReason{Rule: model.ReasonVolume, PerDay: 20, VolumeGrade: "A", BandTo: 50},
			"20 snooping lookups a day (A is under 50)"},
		{"ACR on a laptop still meets a bypass", model.DeviceReport{Device: laptop, PerDay: 20, ByCategory: acr, Bypasses: high},
			"D", model.GradeReason{Rule: model.ReasonBypass, PerDay: 20, VolumeGrade: "A", BandTo: 50, Evidence: "8.8.8.8:443"},
			"Bypasses your DNS via 8.8.8.8:443: at least D (its 20 snooping lookups a day alone would be A)"},
	}
	for _, tt := range tests {
		grade, why := GradeWhy(tt.r)
		if grade != tt.grade || why != tt.why {
			t.Errorf("%s: GradeWhy = %s %+v, want %s %+v", tt.name, grade, why, tt.grade, tt.why)
		}
		if grade != Grade(tt.r) {
			t.Errorf("%s: GradeWhy and Grade disagree", tt.name)
		}
		if got := why.Text(); got != tt.text {
			t.Errorf("%s: Text = %q, want %q", tt.name, got, tt.text)
		}
	}
}

// Kinds that can run content recognition get the ACR rules; the rest do
// not, whatever they look up.
func TestMayRunACR(t *testing.T) {
	for _, k := range []model.DeviceKind{model.KindTV, model.KindStreamer, model.KindUnknown, ""} {
		if !mayRunACR(k) {
			t.Errorf("mayRunACR(%q) = false", k)
		}
	}
	for _, k := range []model.DeviceKind{model.KindPhone, model.KindComputer, model.KindSpeaker, model.KindConsole, model.KindPlug} {
		if mayRunACR(k) {
			t.Errorf("mayRunACR(%q) = true", k)
		}
	}
}

func TestHomeGrade(t *testing.T) {
	devs := func(grades ...string) []model.DeviceReport {
		out := make([]model.DeviceReport, len(grades))
		for i, g := range grades {
			out[i].Grade = g
		}
		return out
	}
	tests := []struct {
		name string
		devs []model.DeviceReport
		want string
	}{
		{"no devices", nil, ""},
		{"one device", devs("B"), "B"},
		{"worst wins", devs("A", "D", "B"), "D"},
		{"F beats D", devs("D", "F", "A"), "F"},
		{"all quiet", devs("A", "A"), "A"},
		{"ungraded devices are ignored", devs("", "C", "?"), "C"},
		{"only ungraded devices", devs(""), ""},
	}
	for _, tt := range tests {
		if got := HomeGrade(tt.devs); got != tt.want {
			t.Errorf("%s: HomeGrade = %q, want %q", tt.name, got, tt.want)
		}
	}
}
