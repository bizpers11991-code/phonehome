package model

import (
	"testing"
	"time"
)

func TestCoverage(t *testing.T) {
	tests := []struct {
		name string
		r    DeviceReport
		want float64
		low  bool
	}{
		{"no lookups", DeviceReport{}, 0, false},
		{"all known", DeviceReport{Total: 10, ByCategory: map[Category]int{CatAds: 4, CatContent: 6}}, 1, false},
		{"half known is not low", DeviceReport{Total: 10, ByCategory: map[Category]int{CatAds: 5, CatUnknown: 5}}, 0.5, false},
		{"mostly unknown", DeviceReport{Total: 10, ByCategory: map[Category]int{CatContent: 4, CatUnknown: 6}}, 0.4, true},
		{"all unknown", DeviceReport{Total: 3, ByCategory: map[Category]int{CatUnknown: 3}}, 0, true},
	}
	for _, tt := range tests {
		if got := tt.r.Coverage(); got != tt.want {
			t.Errorf("%s: Coverage = %v, want %v", tt.name, got, tt.want)
		}
		if got := tt.r.CoverageLow(); got != tt.low {
			t.Errorf("%s: CoverageLow = %v, want %v", tt.name, got, tt.low)
		}
		if got := tt.r.CoverageNote(); (got != "") != tt.low {
			t.Errorf("%s: CoverageNote = %q", tt.name, got)
		}
	}
	r := DeviceReport{Total: 1000, ByCategory: map[Category]int{CatAds: 496, CatUnknown: 504}}
	if got, want := r.CoverageNote(), "Graded on the 49% of lookups phonehome recognises"; got != want {
		t.Errorf("CoverageNote = %q, want %q (rounded down)", got, want)
	}
}

func TestProvisionalText(t *testing.T) {
	tests := []struct {
		g    GradeReason
		want string
	}{
		{GradeReason{Data: 3 * time.Hour}, ""},
		{GradeReason{Provisional: true, Data: 3*time.Hour + 40*time.Minute}, "Provisional: based on 3 hours of data"},
		{GradeReason{Provisional: true, Data: time.Hour}, "Provisional: based on 1 hour of data"},
		{GradeReason{Provisional: true, Data: 20 * time.Minute}, "Provisional: based on less than an hour of data"},
	}
	for _, tt := range tests {
		if got := tt.g.ProvisionalText(); got != tt.want {
			t.Errorf("ProvisionalText(%v) = %q, want %q", tt.g.Data, got, tt.want)
		}
	}
}

func TestACRUnseen(t *testing.T) {
	acr := map[Category]int{CatACR: 1}
	tests := []struct {
		r    DeviceReport
		want bool
	}{
		{DeviceReport{Device: Device{Kind: KindTV}}, true},
		{DeviceReport{Device: Device{Kind: KindStreamer}}, true},
		{DeviceReport{Device: Device{Kind: KindTV}, ByCategory: acr}, false},
		{DeviceReport{Device: Device{Kind: KindPhone}}, false},
		{DeviceReport{Device: Device{Kind: KindUnknown}}, false},
	}
	for _, tt := range tests {
		if got := tt.r.ACRUnseen(); got != tt.want {
			t.Errorf("ACRUnseen(%s, %v) = %v, want %v", tt.r.Device.Kind, tt.r.ByCategory, got, tt.want)
		}
	}
}
