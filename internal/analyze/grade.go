package analyze

import "github.com/bizpers11991-code/phonehome/internal/model"

// Snooping lookups per day at which each grade starts; see docs/grading.md.
const (
	gradeBPerDay = 50
	gradeCPerDay = 300
	gradeDPerDay = 1500
	gradeFPerDay = 5000
)

// Grade scores a device report from A (quiet) to F, following docs/grading.md:
//
//	F  a content-recognition (ACR) heartbeat, or ≥ 5000 snooping lookups/day
//	D  any ACR lookup, ≥ 1500/day, or high-confidence DNS bypass
//	C  ≥ 300/day
//	B  ≥ 50/day
//	A  otherwise
func Grade(r model.DeviceReport) string {
	for _, h := range r.Heartbeats {
		if h.Category == model.CatACR {
			return "F"
		}
	}
	if r.PerDay >= gradeFPerDay {
		return "F"
	}
	if r.ByCategory[model.CatACR] > 0 || r.PerDay >= gradeDPerDay {
		return "D"
	}
	for _, b := range r.Bypasses {
		if b.Confidence == "high" {
			return "D"
		}
	}
	switch {
	case r.PerDay >= gradeCPerDay:
		return "C"
	case r.PerDay >= gradeBPerDay:
		return "B"
	}
	return "A"
}
