package analyze

import (
	"testing"

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
