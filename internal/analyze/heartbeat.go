package analyze

import (
	"cmp"
	"math"
	"slices"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// Heartbeat detection thresholds; docs/detectors.md explains them.
const (
	// burstWindow merges lookups this close to the start of a burst into one
	// moment: A + AAAA + HTTPS queries and retries are one "call", not three.
	burstWindow = 2 * time.Second
	// heartbeatMinSpan is how long the lookups must span to rule out a
	// single session of use.
	heartbeatMinSpan = 6 * time.Hour
	// heartbeatMaxEvery is the slowest cadence we report.
	heartbeatMaxEvery = time.Hour
	// heartbeatMaxJitter is the largest coefficient of variation of the
	// intervals that still counts as "on a clock".
	heartbeatMaxJitter = 0.35
	// heartbeatTrim is the fraction of intervals dropped at each end before
	// measuring, so a night offline or one burst of use does not hide a clock.
	heartbeatTrim = 0.10
)

// detectHeartbeat decides whether lookup times (unix nanoseconds, any order;
// sorted in place) follow a regular clock. It needs at least minEvents
// distinct lookup moments spanning heartbeatMinSpan. It returns the median
// interval, rounded for display, and the coefficient of variation of the
// trimmed intervals.
func detectHeartbeat(times []int64, minEvents int) (every time.Duration, jitter float64, ok bool) {
	minEvents = max(minEvents, 3) // fewer than two intervals is no pattern
	if len(times) < minEvents {
		return 0, 0, false
	}
	slices.Sort(times)
	if time.Duration(times[len(times)-1]-times[0]) < heartbeatMinSpan {
		return 0, 0, false
	}

	events := make([]int64, 0, len(times))
	for _, t := range times {
		if len(events) == 0 || time.Duration(t-events[len(events)-1]) > burstWindow {
			events = append(events, t)
		}
	}
	if len(events) < minEvents {
		return 0, 0, false
	}

	iv := make([]float64, len(events)-1)
	for i := range iv {
		iv[i] = float64(events[i+1] - events[i])
	}
	slices.Sort(iv)
	k := int(float64(len(iv)) * heartbeatTrim)
	iv = iv[k : len(iv)-k]

	var median float64
	if n := len(iv); n%2 == 1 {
		median = iv[n/2]
	} else {
		median = (iv[n/2-1] + iv[n/2]) / 2
	}
	var sum float64
	for _, v := range iv {
		sum += v
	}
	mean := sum / float64(len(iv))
	var ss float64
	for _, v := range iv {
		ss += (v - mean) * (v - mean)
	}
	cv := math.Sqrt(ss/float64(len(iv))) / mean

	m := time.Duration(median)
	if cv > heartbeatMaxJitter || m > heartbeatMaxEvery {
		return 0, 0, false
	}
	return roundEvery(m), cv, true
}

// roundEvery rounds an interval to what a person would say: "every 30s",
// "every 4m10s", "every 15m".
func roundEvery(d time.Duration) time.Duration {
	switch {
	case d < time.Minute:
		return d.Round(time.Second)
	case d < 10*time.Minute:
		return d.Round(10 * time.Second)
	}
	return d.Round(time.Minute)
}

// sortHeartbeats orders snooping destinations first, then by lookup count.
func sortHeartbeats(hs []model.Heartbeat) {
	slices.SortFunc(hs, func(x, y model.Heartbeat) int {
		if xs, ys := x.Category.Snooping(), y.Category.Snooping(); xs != ys {
			if xs {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(y.Count, x.Count), cmp.Compare(x.Domain, y.Domain))
	})
}
