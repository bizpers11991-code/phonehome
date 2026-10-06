package analyze

import (
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// Snooping lookups per day at which each grade starts; see docs/grading.md.
const (
	gradeBPerDay = 50
	gradeCPerDay = 300
	gradeDPerDay = 1500
	gradeFPerDay = 5000
)

// provisionalUnder is how little data makes a grade provisional. It is
// under a day so that a 24-hour view whose first lookup came a few minutes
// in is not flagged.
const provisionalUnder = 20 * time.Hour

// Grade scores a device report from A (quiet) to F; see GradeWhy.
func Grade(r model.DeviceReport) string {
	g, _ := GradeWhy(r)
	return g
}

// GradeWhy scores a device report from A (quiet) to F, following
// docs/grading.md, and returns the rule that decided it:
//
//	F  a content-recognition (ACR) heartbeat, or ≥ 5000 snooping lookups/day
//	D  any ACR lookup, ≥ 1500/day, or high-confidence DNS bypass
//	C  ≥ 300/day
//	B  ≥ 50/day
//	A  otherwise
//
// The ACR rules apply only to kinds that can fingerprint a screen (see
// mayRunACR); elsewhere ACR lookups count as snooping like any other.
// The reason's Data and Provisional depend on the period's data and are
// left for the caller to fill in.
func GradeWhy(r model.DeviceReport) (string, model.GradeReason) {
	why := model.GradeReason{Rule: model.ReasonVolume, PerDay: r.PerDay}
	why.VolumeGrade, why.BandFrom, why.BandTo = volumeGrade(r.PerDay)
	acr := mayRunACR(r.Device.Kind)
	if acr {
		for _, h := range r.Heartbeats {
			if h.Category == model.CatACR {
				why.Rule, why.Evidence, why.Every = model.ReasonACRHeartbeat, h.Domain, h.Every
				return "F", why
			}
		}
	}
	if r.PerDay >= gradeFPerDay {
		return "F", why
	}
	if n := r.ByCategory[model.CatACR]; acr && n > 0 {
		why.Rule, why.Count = model.ReasonACR, n
		return "D", why
	}
	if r.PerDay >= gradeDPerDay {
		return "D", why
	}
	for _, b := range r.Bypasses {
		if b.Confidence == "high" {
			why.Rule, why.Evidence = model.ReasonBypass, b.Evidence
			return "D", why
		}
	}
	return why.VolumeGrade, why
}

// volumeGrade is the grade a rate of snooping lookups per day earns on its
// own, and that grade's range [from, to) (to 0: no upper limit).
func volumeGrade(perDay float64) (grade string, from, to int) {
	switch {
	case perDay >= gradeFPerDay:
		return "F", gradeFPerDay, 0
	case perDay >= gradeDPerDay:
		return "D", gradeDPerDay, gradeFPerDay
	case perDay >= gradeCPerDay:
		return "C", gradeCPerDay, gradeDPerDay
	case perDay >= gradeBPerDay:
		return "B", gradeBPerDay, gradeCPerDay
	}
	return "A", 0, gradeBPerDay
}

// mayRunACR reports whether devices of kind k can run automatic content
// recognition: TVs, streaming players, and devices of unknown kind (which
// may be either). A laptop opening an ACR company's website is not a TV
// fingerprinting its screen.
func mayRunACR(k model.DeviceKind) bool {
	switch k {
	case model.KindTV, model.KindStreamer, model.KindUnknown, "":
		return true
	}
	return false
}

// HomeGrade grades the whole home: it is as good as its worst device, so the
// result is the worst grade among devs, or "" when there are no devices to
// grade. Grades outside A–F (such as "") are ignored. See docs/grading.md.
func HomeGrade(devs []model.DeviceReport) string {
	worst := ""
	for _, d := range devs {
		switch d.Grade {
		case "A", "B", "C", "D", "F":
			if d.Grade > worst {
				worst = d.Grade
			}
		}
	}
	return worst
}
