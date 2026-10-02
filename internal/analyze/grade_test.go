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
