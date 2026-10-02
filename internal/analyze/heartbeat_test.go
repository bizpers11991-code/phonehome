package analyze

import (
	"math/rand/v2"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// series returns n lookup times starting at t0, spaced by step(i).
func series(n int, step func(i int) time.Duration) []int64 {
	out := make([]int64, n)
	t := t0
	for i := range out {
		out[i] = t.UnixNano()
		t = t.Add(step(i))
	}
	return out
}

func every(d time.Duration) func(int) time.Duration { return func(int) time.Duration { return d } }

func TestDetectHeartbeat(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	tests := []struct {
		name      string
		times     []int64
		min       int
		wantOK    bool
		wantEvery time.Duration
	}{
		{"regular 5m for a day", series(288, every(5*time.Minute)), 20, true, 5 * time.Minute},
		{"regular 30m, a week", series(336, every(30*time.Minute)), 20, true, 30 * time.Minute},
		{"exactly 1h", series(24, every(time.Hour)), 20, true, time.Hour},
		{"too slow: 2h", series(48, every(2*time.Hour)), 20, false, 0},
		{"span under 6h", series(60, every(5*time.Minute)), 20, false, 0},
		{"too few lookups", series(19, every(30*time.Minute)), 20, false, 0},
		{
			"small jitter ±10%",
			series(200, func(int) time.Duration { return 270*time.Second + time.Duration(rng.Int64N(int64(60*time.Second))) }),
			20, true, 5 * time.Minute,
		},
		{
			"jittery 1–20m (human use)",
			series(200, func(int) time.Duration { return time.Minute + time.Duration(rng.Int64N(int64(19*time.Minute))) }),
			20, false, 0,
		},
		{
			"regular with a night offline and bursts of use",
			series(300, func(i int) time.Duration {
				switch {
				case i == 100:
					return 8 * time.Hour // unplugged overnight
				case i >= 200 && i < 220:
					return 10 * time.Second // someone using it
				}
				return 10 * time.Minute
			}),
			20, true, 10 * time.Minute,
		},
		{
			// A and AAAA lookups a few ms apart are one call, so the
			// cadence is 15m, not alternating 0 and 15m.
			"A+AAAA pairs collapse",
			series(100, func(i int) time.Duration {
				if i%2 == 0 {
					return 3 * time.Millisecond
				}
				return 15*time.Minute - 3*time.Millisecond
			}),
			20, true, 15 * time.Minute,
		},
		{
			// 30 lookups but only 15 distinct moments.
			"pairs count once toward the minimum",
			series(30, func(i int) time.Duration {
				if i%2 == 0 {
					return time.Millisecond
				}
				return 30 * time.Minute
			}),
			20, false, 0,
		},
		{"unsorted input", reversed(series(50, every(10*time.Minute))), 20, true, 10 * time.Minute},
		{"min below 3 is raised", series(2, every(7*time.Hour)), 1, false, 0},
		{"empty", nil, 20, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, jitter, ok := detectHeartbeat(tt.times, tt.min)
			if ok != tt.wantOK || got != tt.wantEvery {
				t.Fatalf("got (%v, %.3f, %v), want (%v, _, %v)", got, jitter, ok, tt.wantEvery, tt.wantOK)
			}
			if ok && (jitter < 0 || jitter > heartbeatMaxJitter) {
				t.Errorf("jitter = %v out of range", jitter)
			}
		})
	}
}

func reversed(s []int64) []int64 { slices.Reverse(s); return s }

func TestRoundEvery(t *testing.T) {
	tests := []struct{ in, want time.Duration }{
		{29*time.Second + 600*time.Millisecond, 30 * time.Second},
		{4*time.Minute + 14*time.Second, 4*time.Minute + 10*time.Second},
		{14*time.Minute + 51*time.Second, 15 * time.Minute},
		{time.Hour - 20*time.Second, time.Hour},
	}
	for _, tt := range tests {
		if got := roundEvery(tt.in); got != tt.want {
			t.Errorf("roundEvery(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestHeartbeatsInReport(t *testing.T) {
	var qs []model.DNSQuery
	for i := range 200 {
		at := time.Duration(i) * 10 * time.Minute
		qs = append(qs,
			q(at, "10.0.0.5", "time.example"),
			q(at+time.Minute, "10.0.0.5", "acr.samsung.example"))
		if i < 100 { // telemetry: fewer lookups, still regular
			qs = append(qs, q(2*at+2*time.Minute, "10.0.0.5", "log.google.example"))
		}
	}
	devs := []model.Device{{ID: "mac:tv", IPs: []netip.Addr{ip("10.0.0.5")}}}
	d := Analyze(newFakeKB(), week, devs, qs, nil, utcOpt).Devices[0]

	var got []string
	for _, h := range d.Heartbeats {
		got = append(got, h.Domain+" "+h.Every.String())
	}
	// Snooping first (by count), then the essential clock.
	want := []string{"acr.samsung.example 10m0s", "log.google.example 20m0s", "time.example 10m0s"}
	if !slices.Equal(got, want) {
		t.Errorf("heartbeats = %v, want %v", got, want)
	}
	if d.Heartbeats[0].Category != model.CatACR || d.Heartbeats[0].Count != 200 {
		t.Errorf("first heartbeat = %+v", d.Heartbeats[0])
	}
	if d.Grade != "F" {
		t.Errorf("Grade = %s, want F for an ACR heartbeat", d.Grade)
	}
}

func TestMinHeartbeatOption(t *testing.T) {
	var qs []model.DNSQuery
	for i := range 10 {
		qs = append(qs, q(time.Duration(i)*time.Hour, "10.0.0.5", "time.example"))
	}
	if hb := Analyze(newFakeKB(), week, nil, qs, nil, utcOpt).Devices[0].Heartbeats; len(hb) != 0 {
		t.Errorf("default minimum: heartbeats = %+v, want none", hb)
	}
	o := utcOpt
	o.MinHeartbeat = 10
	if hb := Analyze(newFakeKB(), week, nil, qs, nil, o).Devices[0].Heartbeats; len(hb) != 1 {
		t.Errorf("MinHeartbeat 10: heartbeats = %+v, want one", hb)
	}
}
