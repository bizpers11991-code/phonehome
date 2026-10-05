package web

import (
	"bufio"
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

var (
	sampleLine = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{(?:[a-zA-Z_][a-zA-Z0-9_]*="(?:[^"\\\n]|\\["\\n])*",?)*\})? (\S+)$`)
	typeLine   = regexp.MustCompile(`^# TYPE ([a-zA-Z_:][a-zA-Z0-9_:]*) (gauge|counter)$`)
)

// parseExposition checks body against the text exposition format: every
// sample belongs to a family announced by HELP and TYPE just before it, and
// no series appears twice. It returns the samples as "name{labels} value".
func parseExposition(t *testing.T, body []byte) map[string]string {
	t.Helper()
	out := map[string]string{}
	var family string
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "# HELP "):
			continue
		case strings.HasPrefix(line, "# TYPE "):
			m := typeLine.FindStringSubmatch(line)
			if m == nil {
				t.Fatalf("bad TYPE line %q", line)
			}
			family = m[1]
			continue
		}
		m := sampleLine.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("bad sample line %q", line)
		}
		if m[1] != family {
			t.Fatalf("sample %q outside its family %q", line, family)
		}
		series := m[1] + m[2]
		if _, dup := out[series]; dup {
			t.Fatalf("duplicate series %s", series)
		}
		out[series] = m[3]
	}
	return out
}

func TestWriteMetrics(t *testing.T) {
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	rep := model.HomeReport{
		Period:      model.Period{From: at.AddDate(0, 0, -7), To: at},
		GeneratedAt: at,
		Grade:       "D",
		Snooping:    700,
		ByCategory:  map[model.Category]int{model.CatACR: 300, model.CatContent: 50},
		Devices: []model.DeviceReport{
			{
				Device:     model.Device{ID: "mac:aa:bb:cc:00:11:22", Label: `Kid's "TV"` + "\n" + `C:\`, Kind: model.KindTV},
				Grade:      "D",
				PerDay:     100,
				ByCategory: map[model.Category]int{model.CatACR: 300},
				Blocked:    12,
				QuietHours: 40,
				Heartbeats: []model.Heartbeat{{Domain: "acr.example"}},
				Bypasses:   []model.Bypass{{Confidence: "high"}, {Confidence: "medium"}, {Confidence: "high"}},
			},
			{Device: model.Device{ID: "ip:192.168.1.9", IPs: nil}, Grade: "A"},
		},
	}
	st := model.Status{Version: "v0.3.0", Sources: []model.SourceStatus{
		{Name: "pihole", Kind: "dns", LastRun: at, LastOK: at, Records: 1234},
		{Name: "leases", Kind: "devices", LastRun: at, LastError: "permission denied"},
	}}
	got := parseExposition(t, writeMetrics(rep, st, 7))

	tv := `{device="mac:aa:bb:cc:00:11:22",name="Kid's \"TV\"\nC:\\",kind="tv"`
	for series, want := range map[string]string{
		`phonehome_info{version="v0.3.0"}`:             "1",
		`phonehome_demo`:                               "0",
		`phonehome_window_days`:                        "7",
		`phonehome_report_generated_timestamp_seconds`: "1.7909424e+09",
		`phonehome_home_grade`:                         "1",
		`phonehome_home_devices`:                       "2",
		`phonehome_home_lookups{category="acr"}`:       "300",
		`phonehome_home_lookups{category="unknown"}`:   "0",
		`phonehome_home_snooping_lookups_per_day`:      "100",
		`phonehome_device_grade` + tv + `}`:            "1",
		`phonehome_device_grade{device="ip:192.168.1.9",name="ip:192.168.1.9",kind="unknown"}`: "4",
		`phonehome_device_snooping_lookups_per_day` + tv + `}`:                                 "100",
		`phonehome_device_lookups` + tv + `,category="acr"}`:                                   "300",
		`phonehome_device_lookups` + tv + `,category="ads"}`:                                   "0",
		`phonehome_device_blocked_lookups` + tv + `}`:                                          "12",
		`phonehome_device_quiet_hours_lookups` + tv + `}`:                                      "40",
		`phonehome_device_heartbeats` + tv + `}`:                                               "1",
		`phonehome_device_bypass_findings` + tv + `,confidence="high"}`:                        "2",
		`phonehome_device_bypass_findings` + tv + `,confidence="low"}`:                         "0",
		`phonehome_source_up{source="pihole",kind="dns"}`:                                      "1",
		`phonehome_source_up{source="leases",kind="devices"}`:                                  "0",
		`phonehome_source_records{source="pihole",kind="dns"}`:                                 "1234",
		`phonehome_source_last_success_timestamp_seconds{source="pihole",kind="dns"}`:          "1.7909424e+09",
	} {
		if got[series] != want {
			t.Errorf("%s = %q, want %q", series, got[series], want)
		}
	}
	if _, ok := got[`phonehome_source_last_success_timestamp_seconds{source="leases",kind="devices"}`]; ok {
		t.Error("a source that never succeeded has a last-success time")
	}
	// 7 home categories, per device 1 grade + 1 rate + 7 categories + 3
	// counts + 3 confidences, 2 sources × 4 (one without a success time).
	if want := 4 + 1 + 1 + 7 + 1 + 2*(1+1+7+3+3) + 2*4 - 1; len(got) != want {
		t.Errorf("%d series, want %d", len(got), want)
	}
}

func TestWriteMetricsEmpty(t *testing.T) {
	got := parseExposition(t, writeMetrics(model.HomeReport{}, model.Status{Demo: true}, 1))
	if _, ok := got["phonehome_home_grade"]; ok {
		t.Error("home grade without devices")
	}
	if got["phonehome_demo"] != "1" || got["phonehome_home_devices"] != "0" {
		t.Errorf("got %v", got)
	}
}

// A DHCP hostname need not be UTF-8; the exposition format must be, or the
// whole scrape fails.
func TestWriteMetricsInvalidUTF8(t *testing.T) {
	rep := model.HomeReport{Devices: []model.DeviceReport{{
		Device: model.Device{ID: "mac:aa", Hostname: "tv\xff\xfe\"\n"}, Grade: "B",
	}}}
	body := writeMetrics(rep, model.Status{}, 7)
	if !utf8.Valid(body) {
		t.Fatalf("not UTF-8: %q", body)
	}
	parseExposition(t, body)
	if !bytes.Contains(body, []byte(`name="tv`+"�"+`\"\n"`)) {
		t.Errorf("hostname not sanitised and escaped:\n%s", body)
	}
}
