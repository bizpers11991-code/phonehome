package web

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// metricsTTL is how long a computed /metrics page is reused. Building a
// report reads every lookup in the window, and Prometheus may scrape every
// few seconds; the figures barely move in a minute.
const metricsTTL = time.Minute

// gradePoints turns a grade into a number for graphs and alerts, on the
// familiar grade-point scale: A=4, B=3, C=2, D=1, F=0.
var gradePoints = map[string]float64{"A": 4, "B": 3, "C": 2, "D": 1, "F": 0}

// metrics serves the report in the Prometheus text exposition format
// (version 0.0.4), written by hand. Labels are bounded: per device only its
// id, name and kind, plus a category or confidence from small fixed sets;
// never domains or addresses. docs/integrations.md lists every series.
type metrics struct {
	s *server

	mu    sync.Mutex
	cache map[int]metricsPage // by window in days
}

type metricsPage struct {
	body []byte
	at   time.Time
}

func (m *metrics) serve(w http.ResponseWriter, r *http.Request) {
	p, ok := m.s.period(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "days must be 1, 7 or 30")
		return
	}
	days := int(math.Round(p.Days()))
	body, err := m.page(r.Context(), p, days)
	if err != nil {
		m.s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(body)
}

func (m *metrics) page(ctx context.Context, p model.Period, days int) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.s.now()
	if c, ok := m.cache[days]; ok && now.Sub(c.at) < metricsTTL && !now.Before(c.at) {
		return c.body, nil
	}
	rep, err := m.s.b.Report(ctx, p)
	if err != nil {
		return nil, err
	}
	st, err := m.s.b.Status(ctx)
	if err != nil {
		return nil, err
	}
	body := writeMetrics(rep, st, days)
	if m.cache == nil {
		m.cache = map[int]metricsPage{}
	}
	m.cache[days] = metricsPage{body, now}
	return body, nil
}

// family is one metric: its samples are written under a single HELP and
// TYPE line, as the format requires.
type family struct {
	name, help, typ string
	samples         []sample
}

type sample struct {
	labels [][2]string
	value  float64
}

func (f *family) add(v float64, labels ...[2]string) {
	f.samples = append(f.samples, sample{labels, v})
}

func writeMetrics(rep model.HomeReport, st model.Status, days int) []byte {
	g := func(name, help string) *family { return &family{name: name, help: help, typ: "gauge"} }
	var (
		info       = g("phonehome_info", "Always 1; labels say which phonehome produced the figures.")
		demo       = g("phonehome_demo", "1 when the figures come from the built-in demo household, which is synthetic.")
		window     = g("phonehome_window_days", "Length of the period the report figures cover, in days (the days URL parameter).")
		generated  = g("phonehome_report_generated_timestamp_seconds", "When the report behind these figures was computed.")
		homeGrade  = g("phonehome_home_grade", "The home's grade in grade points (A=4, B=3, C=2, D=1, F=0): the worst device grade. Absent with no devices.")
		homeDevs   = g("phonehome_home_devices", "Devices seen in the period.")
		homeTotal  = g("phonehome_home_lookups", "DNS lookups in the period, by category.")
		homeSnoop  = g("phonehome_home_snooping_lookups_per_day", "Snooping lookups (content recognition, ads, tracking, telemetry) per day, whole home.")
		devGrade   = g("phonehome_device_grade", "Device grade in grade points (A=4, B=3, C=2, D=1, F=0). See docs/grading.md.")
		devSnoop   = g("phonehome_device_snooping_lookups_per_day", "Snooping lookups per day for the device.")
		devLookups = g("phonehome_device_lookups", "DNS lookups by the device in the period, by category.")
		devBlocked = g("phonehome_device_blocked_lookups", "Lookups by the device that the DNS filter blocked in the period.")
		devQuiet   = g("phonehome_device_quiet_hours_lookups", "Lookups by the device during the quiet-hours window in the period.")
		devBeats   = g("phonehome_device_heartbeats", "Destinations the device contacted on a regular clock (heartbeats) in the period.")
		devBypass  = g("phonehome_device_bypass_findings", "DNS-bypass findings for the device, by confidence.")
		srcUp      = g("phonehome_source_up", "1 if the source's last ingestion run succeeded, 0 if it failed.")
		srcRecords = g("phonehome_source_records", "Records ingested from the source since phonehome's database was created; for a device source, the devices it listed last time.")
		srcLastRun = g("phonehome_source_last_run_timestamp_seconds", "When the source was last read.")
		srcLastOK  = g("phonehome_source_last_success_timestamp_seconds", "When the source was last read without error. Absent if never.")
	)

	info.add(1, [2]string{"version", st.Version})
	demo.add(boolValue(rep.Demo || st.Demo))
	window.add(float64(days))
	generated.add(unixSeconds(rep.GeneratedAt))
	if v, ok := gradePoints[rep.Grade]; ok {
		homeGrade.add(v)
	}
	homeDevs.add(float64(len(rep.Devices)))
	for _, c := range model.Categories() {
		homeTotal.add(float64(rep.ByCategory[c]), [2]string{"category", string(c)})
	}
	homeSnoop.add(float64(rep.Snooping) / rep.Period.Days())

	devs := slices.Clone(rep.Devices)
	slices.SortFunc(devs, func(a, b model.DeviceReport) int { return cmp.Compare(a.Device.ID, b.Device.ID) })
	for _, d := range devs {
		kind := d.Device.Kind
		if kind == "" {
			kind = model.KindUnknown
		}
		id := [][2]string{{"device", d.Device.ID}, {"name", d.Device.DisplayName()}, {"kind", string(kind)}}
		with := func(k, v string) [][2]string { return append(slices.Clone(id), [2]string{k, v}) }
		if v, ok := gradePoints[d.Grade]; ok {
			devGrade.add(v, id...)
		}
		devSnoop.add(d.PerDay, id...)
		for _, c := range model.Categories() {
			devLookups.add(float64(d.ByCategory[c]), with("category", string(c))...)
		}
		devBlocked.add(float64(d.Blocked), id...)
		devQuiet.add(float64(d.QuietHours), id...)
		devBeats.add(float64(len(d.Heartbeats)), id...)
		byConf := map[string]int{}
		for _, b := range d.Bypasses {
			byConf[b.Confidence]++
		}
		for _, c := range []string{"high", "medium", "low"} {
			devBypass.add(float64(byConf[c]), with("confidence", c)...)
		}
	}

	srcs := slices.Clone(st.Sources)
	slices.SortFunc(srcs, func(a, b model.SourceStatus) int { return cmp.Compare(a.Name, b.Name) })
	for _, s := range srcs {
		l := [][2]string{{"source", s.Name}, {"kind", s.Kind}}
		srcUp.add(boolValue(s.LastError == ""), l...)
		srcRecords.add(float64(s.Records), l...)
		if !s.LastRun.IsZero() {
			srcLastRun.add(unixSeconds(s.LastRun), l...)
		}
		if !s.LastOK.IsZero() {
			srcLastOK.add(unixSeconds(s.LastOK), l...)
		}
	}

	var b strings.Builder
	for _, f := range []*family{info, demo, window, generated, homeGrade, homeDevs, homeTotal, homeSnoop,
		devGrade, devSnoop, devLookups, devBlocked, devQuiet, devBeats, devBypass,
		srcUp, srcRecords, srcLastRun, srcLastOK} {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", f.name, f.help, f.name, f.typ)
		for _, s := range f.samples {
			b.WriteString(f.name)
			if len(s.labels) > 0 {
				b.WriteByte('{')
				for i, l := range s.labels {
					if i > 0 {
						b.WriteByte(',')
					}
					b.WriteString(l[0])
					b.WriteString(`="`)
					b.WriteString(escapeLabel(l[1]))
					b.WriteByte('"')
				}
				b.WriteByte('}')
			}
			b.WriteByte(' ')
			b.WriteString(formatValue(s.value))
			b.WriteByte('\n')
		}
	}
	return []byte(b.String())
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// escapeLabel escapes a label value as the text format requires: backslash,
// double quote and line feed. Device names are user-chosen labels.
func escapeLabel(s string) string { return labelEscaper.Replace(s) }

func formatValue(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func unixSeconds(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.UnixMilli()) / 1000
}
