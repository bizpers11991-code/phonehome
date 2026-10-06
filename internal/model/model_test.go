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

// CoveragePercent rounds down, in integers: 29 of 100 is 29%, not the
// 28% that floor(0.29 * 100) gives in floating point.
func TestCoveragePercent(t *testing.T) {
	tests := []struct {
		total, unknown, want int
	}{
		{0, 0, 0},
		{100, 71, 29},
		{100, 43, 57},
		{1000, 504, 49}, // 49.6%
		{1000, 500, 50},
		{3, 1, 66},
		{3, 0, 100},
		{7, 7, 0},
	}
	for _, tt := range tests {
		r := DeviceReport{Total: tt.total, ByCategory: map[Category]int{CatUnknown: tt.unknown}}
		if got := r.CoveragePercent(); got != tt.want {
			t.Errorf("CoveragePercent(%d of %d unknown) = %d, want %d", tt.unknown, tt.total, got, tt.want)
		}
	}
}

func TestGradeReasonText(t *testing.T) {
	tests := []struct {
		g    GradeReason
		want string
	}{
		{GradeReason{}, ""},
		{GradeReason{Rule: ReasonVolume, PerDay: 0, VolumeGrade: "A", BandTo: 50}, "0 snooping lookups a day (A is under 50)"},
		{GradeReason{Rule: ReasonVolume, PerDay: 1.4, VolumeGrade: "A", BandTo: 50}, "1 snooping lookup a day (A is under 50)"},
		// Rounding never carries a rate across its band's edge.
		{GradeReason{Rule: ReasonVolume, PerDay: 49.6, VolumeGrade: "A", BandTo: 50}, "49 snooping lookups a day (A is under 50)"},
		{GradeReason{Rule: ReasonVolume, PerDay: 1499.5, VolumeGrade: "C", BandFrom: 300, BandTo: 1500}, "1,499 snooping lookups a day (C is 300–1,499)"},
		{GradeReason{Rule: ReasonVolume, PerDay: 12345, VolumeGrade: "F", BandFrom: 5000}, "12,345 snooping lookups a day (F is 5,000 or more)"},
		{GradeReason{Rule: ReasonACR, Count: 1}, "Contacts content-recognition (ACR) servers (1 lookup): at least D"},
		{GradeReason{Rule: ReasonACR, Count: 1204}, "Contacts content-recognition (ACR) servers (1,204 lookups): at least D"},
		{GradeReason{Rule: ReasonBypass, PerDay: 1, VolumeGrade: "A", BandTo: 50, Evidence: "1.1.1.1:853"},
			"Bypasses your DNS via 1.1.1.1:853: at least D (its 1 snooping lookup a day alone would be A)"},
		{GradeReason{Rule: ReasonACRHeartbeat, Every: 15 * time.Second}, "Content recognition (ACR) on a clock, every 15s: always F"},
		{GradeReason{Rule: ReasonACRHeartbeat, Every: time.Minute}, "Content recognition (ACR) on a clock, every 1 min: always F"},
		{GradeReason{Rule: ReasonACRHeartbeat, Every: 2 * time.Minute}, "Content recognition (ACR) on a clock, every 2 min: always F"},
		{GradeReason{Rule: ReasonACRHeartbeat, Every: 2 * time.Hour}, "Content recognition (ACR) on a clock, every 2 h: always F"},
		{GradeReason{Rule: ReasonACRHeartbeat, Every: 48 * time.Hour}, "Content recognition (ACR) on a clock, every 2 days: always F"},
		{GradeReason{Rule: ReasonACRHeartbeat}, "Content recognition (ACR) on a clock, regularly: always F"},
	}
	for _, tt := range tests {
		if got := tt.g.Text(); got != tt.want {
			t.Errorf("Text(%+v) = %q, want %q", tt.g, got, tt.want)
		}
	}

	// The receipt prints small rates to a decimal place.
	g := GradeReason{Rule: ReasonVolume, PerDay: 1.4, VolumeGrade: "A", BandTo: 50}
	if got, want := g.TextRate("1.4"), "1.4 snooping lookups a day (A is under 50)"; got != want {
		t.Errorf("TextRate = %q, want %q", got, want)
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
